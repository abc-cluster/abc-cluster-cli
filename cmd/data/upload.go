package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
	"github.com/bdragon300/tusgo"
	"github.com/spf13/cobra"
)

const (
	bytesPerMB         = 1024 * 1024
	resumeStateSubpath = "abc-cluster-cli/tus-resume"
	preflightTimeout   = 5 * time.Second

	// defaultUploadChunkSize is the tus chunk size used when the caller does
	// not provide --chunk-size.  64 MiB gives ~1,600 PATCH requests for a
	// 100 GiB file, balancing request overhead against server buffer pressure.
	// Small files (<64 MiB) will be sent as a single chunk.
	defaultUploadChunkSize = 64 * 1024 * 1024
)

type uploadOptions struct {
	filePath      string
	name          string
	endpoint      string
	cryptPassword string
	cryptSalt     string
	token         string
	checksum      bool
	progress      bool
	parallel      bool
	parallelJobs  int
	rawChunkSize  string   // e.g. "64MB"
	rawMaxRate    string   // e.g. "50MB/s"
	meta          []string // e.g. ["key1=val1"]
	noResume      bool
	status        bool // show stored resume state; skip actual upload
	clear         bool // clear stored resume state; skip actual upload
}

type uploadProgressContextKey struct{}

func inputError(format string, args ...interface{}) error {
	return fmt.Errorf("input error: "+format, args...)
}

func authError(format string, args ...interface{}) error {
	return fmt.Errorf("auth/config error: "+format, args...)
}

func networkError(format string, args ...interface{}) error {
	return fmt.Errorf("network/server error: "+format, args...)
}

func localIOError(format string, args ...interface{}) error {
	return fmt.Errorf("local io error: "+format, args...)
}

func withUploadProgress(ctx context.Context, onProgress func(int64)) context.Context {
	if onProgress == nil {
		return ctx
	}
	return context.WithValue(ctx, uploadProgressContextKey{}, onProgress)
}

func uploadProgressFromContext(ctx context.Context) func(int64) {
	v := ctx.Value(uploadProgressContextKey{})
	if v == nil {
		return nil
	}
	onProgress, _ := v.(func(int64))
	return onProgress
}

type uploadOutputContextKey struct{}

func withUploadOutput(ctx context.Context, w io.Writer) context.Context {
	if w == nil {
		return ctx
	}
	return context.WithValue(ctx, uploadOutputContextKey{}, w)
}

func uploadOutputFromContext(ctx context.Context) io.Writer {
	v := ctx.Value(uploadOutputContextKey{})
	if v == nil {
		return nil
	}
	w, _ := v.(io.Writer)
	return w
}

// uploadPrintfContextKey carries a print function that routes messages through
// the bubbletea program (via program.Printf) so they appear above the TUI bar
// rather than being overwritten on the next render cycle.
type uploadPrintfContextKey struct{}

func withUploadPrintf(ctx context.Context, fn func(string, ...interface{})) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, uploadPrintfContextKey{}, fn)
}

func uploadPrintfFromContext(ctx context.Context) func(string, ...interface{}) {
	v := ctx.Value(uploadPrintfContextKey{})
	if v == nil {
		return nil
	}
	fn, _ := v.(func(string, ...interface{}))
	return fn
}

type uploadPreflightChecker interface {
	PreflightNetwork(ctx context.Context) error
}

// newUploadCmd returns the "data upload" subcommand.
func newUploadCmd(serverURL, accessToken, workspace *string, factory ClientFactory) *cobra.Command {
	opts := &uploadOptions{}

	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a file using tus",
		Long: `Upload a file or folder to the abc-cluster data service using the tus resumable upload protocol.

Examples:
  # Upload a local file
  abc data upload ./data.csv

  # Upload with a display name
  abc data upload ./data.csv --name sample-data

  # Upload all files from a folder
  abc data upload ./dataset`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.filePath = args[0]
			return runUpload(cmd, opts, *serverURL, *accessToken, factory)
		},
	}

	cmd.Flags().StringVar(&opts.name, "name", "", "display name for the uploaded file")
	cmd.Flags().StringVar(&opts.endpoint, "endpoint", "", "tus upload endpoint URL (or set ABC_UPLOAD_ENDPOINT / context upload_endpoint; defaults to <url>/files/ from API --url or context endpoint)")
	cmd.Flags().StringVar(&opts.cryptPassword, "crypt-password", "", "rclone crypt password for client-side encryption")
	cmd.Flags().StringVar(&opts.cryptSalt, "crypt-salt", "", "rclone crypt salt (password2) for client-side encryption")
	cmd.Flags().StringVar(&opts.token, "upload-token", "", "bearer token for tus uploads (or set ABC_UPLOAD_TOKEN / context upload_token; falls back to --access-token)")
	cmd.Flags().BoolVar(&opts.checksum, "checksum", true, "include sha256 checksum metadata in tus upload metadata")
	cmd.Flags().BoolVar(&opts.progress, "progress", true, "show live progress bars for encryption and uploads")
	cmd.Flags().BoolVar(&opts.parallel, "parallel", true, "upload directory files in parallel")
	cmd.Flags().IntVar(&opts.parallelJobs, "parallel-jobs", runtime.NumCPU(), "number of parallel upload workers when --parallel=true")
	cmd.Flags().StringVar(&opts.rawChunkSize, "chunk-size", "", `upload chunk size (e.g. 64MB, 2MiB); default is the library default (~2 MB)`)
	cmd.Flags().StringVar(&opts.rawMaxRate, "max-rate", "", `maximum upload throughput (e.g. 50MB/s, 10MiB/s); default is unlimited`)
	cmd.Flags().StringArrayVar(&opts.meta, "meta", nil, `additional tus upload metadata as key=value (repeatable, e.g. --meta project=abc)`)
	cmd.Flags().BoolVar(&opts.noResume, "no-resume", false, "ignore stored resume state and always start a fresh upload")
	cmd.Flags().BoolVar(&opts.status, "status", false, "show stored tus resume state for the file (does not upload)")
	cmd.Flags().BoolVar(&opts.clear, "clear", false, "clear stored tus resume state for the file (does not upload)")

	return cmd
}

func runUpload(cmd *cobra.Command, opts *uploadOptions, serverURL, accessToken string, factory ClientFactory) error {
	if strings.TrimSpace(opts.filePath) == "" {
		return inputError("upload path is empty; provide a local file or directory path")
	}

	if opts.status && opts.clear {
		return inputError("--status and --clear cannot be used together")
	}

	if opts.status || opts.clear {
		endpoint, err := resolveUploadEndpoint(cmd, opts.endpoint, serverURL)
		if err != nil {
			return err
		}
		statePath, err := uploadResumeStatePrimaryPath(endpoint, opts.filePath)
		if err != nil {
			return fmt.Errorf("build resume state path: %w", err)
		}
		if opts.clear {
			if err := clearUploadResumeLocation(statePath); err != nil {
				return fmt.Errorf("clear resume state: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Resume state cleared for %q\n", opts.filePath)
			return nil
		}
		// --status
		location, ok, err := loadUploadResumeLocation(statePath)
		if err != nil {
			return fmt.Errorf("load resume state: %w", err)
		}
		if !ok {
			fmt.Fprintf(cmd.OutOrStdout(), "No resume state found for %q\n", opts.filePath)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "File:       %s\n", opts.filePath)
		fmt.Fprintf(cmd.OutOrStdout(), "Endpoint:   %s\n", endpoint)
		fmt.Fprintf(cmd.OutOrStdout(), "Resume URL: %s\n", location)
		fmt.Fprintf(cmd.OutOrStdout(), "State file: %s\n", statePath)
		return nil
	}

	info, err := os.Stat(opts.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return inputError("path %q does not exist; verify the path and try again", opts.filePath)
		}
		if errors.Is(err, os.ErrPermission) {
			return inputError("permission denied while accessing %q; check file permissions", opts.filePath)
		}
		return localIOError("failed to access path %q: %w", opts.filePath, err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return inputError("path %q is not a regular file; only files and directories are supported", opts.filePath)
	}
	isDir := info.IsDir()
	if isDir {
		if opts.name != "" {
			return inputError("--name can only be used when uploading a single file")
		}
		jobs := opts.parallelJobs
		if !opts.parallel {
			jobs = 1
		}
		if jobs < 1 {
			return inputError("--parallel-jobs must be >= 1")
		}
	}

	endpoint, err := resolveUploadEndpoint(cmd, opts.endpoint, serverURL)
	if err != nil {
		return err
	}

	cryptor, err := uploadCryptConfig(opts.cryptPassword, opts.cryptSalt)
	if err != nil {
		return err
	}

	authToken := resolveUploadToken(cmd, opts.token, accessToken)

	var chunkSize int64
	if opts.rawChunkSize != "" {
		var parseErr error
		chunkSize, parseErr = parseByteCount(opts.rawChunkSize)
		if parseErr != nil {
			return inputError("invalid --chunk-size %q: %w", opts.rawChunkSize, parseErr)
		}
	}

	var maxRate int64
	if opts.rawMaxRate != "" {
		var parseErr error
		maxRate, parseErr = parseMaxRate(opts.rawMaxRate)
		if parseErr != nil {
			return inputError("invalid --max-rate %q: %w", opts.rawMaxRate, parseErr)
		}
	}

	extraMeta, err := parseMetaFlags(opts.meta)
	if err != nil {
		return inputError("invalid --meta flag: %w", err)
	}

	uploaderOpts := UploaderOptions{
		ChunkSize: chunkSize,
		MaxRate:   maxRate,
		NoResume:  opts.noResume,
	}

	uploader, err := factory(endpoint, authToken, uploaderOpts)
	if err != nil {
		return fmt.Errorf("failed to initialize upload client: %w", err)
	}
	if checker, ok := uploader.(uploadPreflightChecker); ok {
		if err := checker.PreflightNetwork(cmd.Context()); err != nil {
			return networkError("pre-flight network check failed: %w", err)
		}
	}

	if isDir {
		jobs := opts.parallelJobs
		if !opts.parallel {
			jobs = 1
		}
		return uploadDirectory(cmd, uploader, opts.filePath, cryptor, opts.checksum, opts.progress, jobs, extraMeta)
	}

	return uploadSingleFile(cmd, uploader, opts.filePath, opts.name, info.Size(), cryptor, opts.checksum, opts.progress, extraMeta)
}

func resolveEndpoint(endpoint, serverURL string) (string, error) {
	if endpoint == "" {
		return buildDefaultEndpoint(serverURL)
	}
	return endpoint, nil
}

func buildDefaultEndpoint(serverURL string) (string, error) {
	out, err := abccfg.DeriveUploadEndpointFromAPI(serverURL)
	if err != nil {
		return "", inputError("invalid server URL %q: %w", serverURL, err)
	}
	return out, nil
}

type uploadResult struct {
	relPath  string
	sizeMB   string
	checksum string
	err      error
}

func uploadDirectory(cmd *cobra.Command, uploader Uploader, dir string, cryptor *cryptConfig, checksumEnabled bool, progressEnabled bool, jobs int, extraMeta map[string]string) error {
	files, err := collectFiles(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no files found in directory %q", dir)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Uploading %d files...\n", len(files))
	if jobs <= 1 {
		for _, file := range files {
			result := uploadDirectoryFile(cmd.Context(), cmd.OutOrStdout(), uploader, dir, file, cryptor, checksumEnabled, progressEnabled, extraMeta)
			if result.err != nil {
				return result.err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Uploaded %s\n", result.relPath)
			fmt.Fprintf(cmd.OutOrStdout(), "  Size: %s\n", result.sizeMB)
			if checksumEnabled {
				fmt.Fprintf(cmd.OutOrStdout(), "  Checksum: %s\n", result.checksum)
			}
		}
		return nil
	}

	jobsCh := make(chan uploadFile)
	resultsCh := make(chan uploadResult, len(files))
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobsCh {
				result := uploadDirectoryFile(ctx, cmd.OutOrStdout(), uploader, dir, file, cryptor, checksumEnabled, false, extraMeta)
				if result.err != nil {
					cancel()
				}
				resultsCh <- result
			}
		}()
	}

submitLoop:
	for _, file := range files {
		select {
		case <-ctx.Done():
			break submitLoop
		case jobsCh <- file:
		}
	}
	close(jobsCh)
	wg.Wait()
	close(resultsCh)

	var firstErr error
	for result := range resultsCh {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Uploaded %s\n", result.relPath)
		fmt.Fprintf(cmd.OutOrStdout(), "  Size: %s\n", result.sizeMB)
		if checksumEnabled {
			fmt.Fprintf(cmd.OutOrStdout(), "  Checksum: %s\n", result.checksum)
		}
	}

	if firstErr != nil {
		return firstErr
	}

	return nil
}

func uploadDirectoryFile(ctx context.Context, out io.Writer, uploader Uploader, dir string, file uploadFile, cryptor *cryptConfig, checksumEnabled bool, progressEnabled bool, extraMeta map[string]string) uploadResult {
	result := uploadResult{}
	relPath, err := filepath.Rel(dir, file.path)
	if err != nil {
		result.err = localIOError("failed to resolve path for %q: %w", file.path, err)
		return result
	}
	result.relPath = relPath

	metadata := make(map[string]string, len(extraMeta)+3)
	for k, v := range extraMeta {
		metadata[k] = v
	}
	metadata["filename"] = filepath.Base(file.path)
	metadata["relativePath"] = filepath.ToSlash(relPath)
	encryptProgress := newProgressReporter(out, progressEnabled && cryptor != nil, fmt.Sprintf("Encrypting %s", relPath), file.size)
	uploadPath, cleanup, err := encryptForUpload(ctx, file.path, cryptor, func(n int64) {
		encryptProgress.Add(n)
	})
	encryptDoneErr := encryptProgress.Complete()
	if encryptDoneErr != nil {
		result.err = fmt.Errorf("failed to render encryption progress: %w", encryptDoneErr)
		return result
	}
	if err != nil {
		result.err = localIOError("data encryption failed for %q: %w", relPath, err)
		return result
	}
	if !checksumEnabled {
		metadata["checksum"] = ""
	}
	uploadInfo, err := os.Stat(uploadPath)
	if err != nil {
		result.err = localIOError("failed to access upload file for %q: %w", relPath, err)
		return result
	}
	if checksumEnabled {
		checksumValue, err := fileSHA256(ctx, uploadPath)
		if err != nil {
			result.err = localIOError("failed to compute checksum for %q: %w", relPath, err)
			return result
		}
		result.checksum = "sha256:" + checksumValue
		metadata["checksum"] = result.checksum
	}
	uploadProgress := newProgressReporter(out, progressEnabled, fmt.Sprintf("Uploading %s", relPath), uploadInfo.Size())
	uploadCtx := withUploadOutput(ctx, out)
	if uploadProgress.enabled {
		uploadCtx = withUploadPrintf(uploadCtx, func(format string, args ...interface{}) {
			uploadProgress.Printf(format, args...)
		})
	}
	log := debuglog.FromContext(ctx)
	_, err = uploader.Upload(withUploadProgress(uploadCtx, func(n int64) {
		uploadProgress.Add(n)
	}), uploadPath, metadata)
	uploadDoneErr := uploadProgress.Complete()
	if uploadDoneErr != nil {
		result.err = fmt.Errorf("failed to render upload progress: %w", uploadDoneErr)
		return result
	}
	if cleanup != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			result.err = fmt.Errorf("failed to clean up encrypted file for %q: %w", relPath, cleanupErr)
			return result
		}
	}
	if err != nil {
		log.LogAttrs(ctx, debuglog.L1, "data.upload.failed",
			debuglog.AttrsError("data.upload", err)...,
		)
		result.err = fmt.Errorf("data upload failed for %q: %w", relPath, err)
		return result
	}
	log.LogAttrs(ctx, debuglog.L1, "data.upload.complete",
		debuglog.AttrsDataUpload(file.path, "", uploadInfo.Size(), "tus-directory")...,
	)

	result.sizeMB = formatSizeMB(uploadInfo.Size())
	return result
}

func uploadSingleFile(cmd *cobra.Command, uploader Uploader, filePath, name string, size int64, cryptor *cryptConfig, checksumEnabled bool, progressEnabled bool, extraMeta map[string]string) error {
	metadata := make(map[string]string, len(extraMeta)+3)
	for k, v := range extraMeta {
		metadata[k] = v
	}
	metadata["filename"] = filepath.Base(filePath)
	if name != "" {
		metadata["name"] = name
	}
	if !checksumEnabled {
		metadata["checksum"] = ""
	}

	encryptProgress := newProgressReporter(cmd.OutOrStdout(), progressEnabled && cryptor != nil, fmt.Sprintf("Encrypting %s", filepath.Base(filePath)), size)
	uploadPath, cleanup, err := encryptForUpload(cmd.Context(), filePath, cryptor, func(n int64) {
		encryptProgress.Add(n)
	})
	encryptDoneErr := encryptProgress.Complete()
	if encryptDoneErr != nil {
		return fmt.Errorf("failed to render encryption progress: %w", encryptDoneErr)
	}
	if err != nil {
		return fmt.Errorf("data encryption failed: %w", err)
	}
	uploadInfo, err := os.Stat(uploadPath)
	if err != nil {
		return fmt.Errorf("failed to access upload file: %w", err)
	}
	var localChecksum string
	if checksumEnabled {
		checksumValue, err := fileSHA256(cmd.Context(), uploadPath)
		if err != nil {
			return fmt.Errorf("failed to compute checksum: %w", err)
		}
		localChecksum = "sha256:" + checksumValue
		metadata["checksum"] = localChecksum
	}
	uploadProgress := newProgressReporter(cmd.OutOrStdout(), progressEnabled, fmt.Sprintf("Uploading %s", filepath.Base(filePath)), uploadInfo.Size())
	uploadCtx := withUploadOutput(cmd.Context(), cmd.OutOrStdout())
	if uploadProgress.enabled {
		uploadCtx = withUploadPrintf(uploadCtx, func(format string, args ...interface{}) {
			uploadProgress.Printf(format, args...)
		})
	}
	log := debuglog.FromContext(cmd.Context())
	_, err = uploader.Upload(withUploadProgress(uploadCtx, func(n int64) {
		uploadProgress.Add(n)
	}), uploadPath, metadata)
	uploadDoneErr := uploadProgress.Complete()
	if uploadDoneErr != nil {
		return fmt.Errorf("failed to render upload progress: %w", uploadDoneErr)
	}
	if cleanup != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return fmt.Errorf("failed to clean up encrypted file: %w", cleanupErr)
		}
	}
	if err != nil {
		log.LogAttrs(cmd.Context(), debuglog.L1, "data.upload.failed",
			debuglog.AttrsError("data.upload", err)...,
		)
		return networkError("data upload failed: %w", err)
	}
	log.LogAttrs(cmd.Context(), debuglog.L1, "data.upload.complete",
		debuglog.AttrsDataUpload(filePath, "", uploadInfo.Size(), "tus-single")...,
	)

	fmt.Fprintln(cmd.OutOrStdout(), "File uploaded successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Size: %s\n", formatSizeMB(uploadInfo.Size()))
	if checksumEnabled {
		fmt.Fprintf(cmd.OutOrStdout(), "  Checksum: %s\n", localChecksum)
	}

	return nil
}

func fileSHA256(ctx context.Context, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return sha256Hex(ctx, file)
}

func formatSizeMB(sizeBytes int64) string {
	if sizeBytes < 0 {
		sizeBytes = 0
	}
	mb := float64(sizeBytes) / bytesPerMB
	if math.Round(mb*100)/100 == 0 {
		return fmt.Sprintf("%d bytes", sizeBytes)
	}
	return fmt.Sprintf("%.2f MB", mb)
}

type uploadFile struct {
	path string
	size int64
}

func collectFiles(root string) ([]uploadFile, error) {
	var files []uploadFile
	err := filepath.WalkDir(root, func(entryPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, uploadFile{path: entryPath, size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan directory %q: %w", root, err)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].path < files[j].path
	})
	return files, nil
}

type tusUploader struct {
	client      *tusgo.Client
	opts        UploaderOptions
	endpoint    string
	accessToken string
}

func newTusUploader(endpoint, accessToken string, opts UploaderOptions) (Uploader, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid upload endpoint %q: %w", endpoint, err)
	}

	client := tusgo.NewClient(http.DefaultClient, parsed)
	if accessToken != "" {
		client.GetRequest = func(method, requestURL string, body io.Reader, _ *tusgo.Client, _ *http.Client) (*http.Request, error) {
			req, err := http.NewRequest(method, requestURL, body)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+accessToken)
			return req, nil
		}
	}
	return &tusUploader{
		client:      client,
		opts:        opts,
		endpoint:    parsed.String(),
		accessToken: accessToken,
	}, nil
}

func (u *tusUploader) PreflightNetwork(ctx context.Context) error {
	endpoint := strings.TrimSpace(u.endpoint)
	if endpoint == "" {
		return fmt.Errorf("upload endpoint is empty; provide --endpoint or ABC_UPLOAD_ENDPOINT")
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid upload endpoint %q: %w", endpoint, err)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("upload endpoint %q is missing a host", endpoint)
	}

	port := parsed.Port()
	switch parsed.Scheme {
	case "http":
		if port == "" {
			port = "80"
		}
	case "https":
		if port == "" {
			port = "443"
		}
	default:
		return fmt.Errorf("upload endpoint %q uses unsupported scheme %q; use http or https", endpoint, parsed.Scheme)
	}

	checkCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	if ip := net.ParseIP(host); ip == nil && !strings.EqualFold(host, "localhost") {
		if _, err := net.DefaultResolver.LookupHost(checkCtx, host); err != nil {
			return explainPreflightNetworkError("DNS lookup", endpoint, host, err)
		}
	}

	address := net.JoinHostPort(host, port)
	conn, err := (&net.Dialer{}).DialContext(checkCtx, "tcp", address)
	if err != nil {
		return explainPreflightNetworkError("TCP connect", endpoint, address, err)
	}
	_ = conn.Close()

	req, err := http.NewRequestWithContext(checkCtx, http.MethodOptions, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build OPTIONS request for upload endpoint %q: %w", endpoint, err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	if token := strings.TrimSpace(u.accessToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return explainPreflightNetworkError("OPTIONS request", endpoint, endpoint, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("upload endpoint %q rejected authentication (HTTP %d); provide a valid --upload-token/ABC_UPLOAD_TOKEN or --access-token", endpoint, resp.StatusCode)
	case http.StatusNotFound:
		return fmt.Errorf("upload endpoint %q returned HTTP 404 for OPTIONS; ensure --endpoint/ABC_UPLOAD_ENDPOINT points to a tus upload root (often with a trailing slash)", endpoint)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("upload endpoint %q returned HTTP %d during OPTIONS; the upload service may be unavailable", endpoint, resp.StatusCode)
	}
	if strings.TrimSpace(resp.Header.Get("Tus-Version")) == "" {
		return fmt.Errorf("upload endpoint %q is reachable but did not return Tus-Version on OPTIONS; ensure this is a tus upload endpoint", endpoint)
	}

	return nil
}

func explainPreflightNetworkError(operation, endpoint, target string, err error) error {
	if err == nil {
		return nil
	}

	rootErr := err
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		rootErr = urlErr.Err
	}

	if errors.Is(rootErr, context.DeadlineExceeded) {
		return fmt.Errorf("%s to %q timed out while checking %q; verify network connectivity, VPN/proxy settings, and endpoint reachability", operation, target, endpoint)
	}
	if errors.Is(rootErr, context.Canceled) {
		return rootErr
	}

	var dnsErr *net.DNSError
	if errors.As(rootErr, &dnsErr) {
		host := strings.TrimSpace(dnsErr.Name)
		if host == "" {
			host = target
		}
		return fmt.Errorf("cannot resolve upload host %q from endpoint %q: %v; verify --endpoint/ABC_UPLOAD_ENDPOINT and DNS settings", host, endpoint, dnsErr)
	}

	low := strings.ToLower(rootErr.Error())
	if strings.Contains(low, "connection refused") {
		return fmt.Errorf("cannot connect to upload endpoint %q (%s): connection refused; verify host/port and that the upload service is running", endpoint, target)
	}
	if strings.Contains(low, "no route to host") || strings.Contains(low, "network is unreachable") {
		return fmt.Errorf("cannot route traffic to upload endpoint %q (%s): %v; verify network routes, VPN, and firewall rules", endpoint, target, rootErr)
	}
	if strings.Contains(low, "tls") {
		return fmt.Errorf("TLS handshake failed for upload endpoint %q: %v; verify https certificate trust and endpoint scheme", endpoint, rootErr)
	}

	return fmt.Errorf("%s failed for upload endpoint %q (%s): %v; verify --endpoint/ABC_UPLOAD_ENDPOINT and network access", operation, endpoint, target, rootErr)
}

func (u *tusUploader) Upload(ctx context.Context, filePath string, metadata map[string]string) (string, error) {
	const maxResumeAttempts = 3

	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}

	metadataWithChecksum := copyMetadata(metadata)
	checksumRaw, checksumProvided := metadataWithChecksum["checksum"]
	if checksumProvided {
		if strings.TrimSpace(checksumRaw) == "" {
			delete(metadataWithChecksum, "checksum")
		}
	} else {
		checksumValue, err := sha256Hex(ctx, file)
		if err != nil {
			return "", fmt.Errorf("compute checksum: %w", err)
		}
		metadataWithChecksum["checksum"] = "sha256:" + checksumValue
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek file: %w", err)
	}

	client := u.client.WithContext(ctx)

	upload := tusgo.Upload{}
	resumeStatePath, legacyResumeStatePath, resumePathErr := uploadResumeStatePaths(client.BaseURL.String(), filePath, info.Size(), info.ModTime().UnixNano())
	if resumePathErr != nil {
		return "", fmt.Errorf("build upload resume state path: %w", resumePathErr)
	}

	// makeStream creates a new UploadStream with the effective chunk size applied.
	// When the caller did not specify --chunk-size the 64 MiB default is used,
	// which keeps request count manageable for files up to 100 GiB.
	makeStream := func() *tusgo.UploadStream {
		s := tusgo.NewUploadStream(client, &upload)
		chunkSize := u.opts.ChunkSize
		if chunkSize <= 0 {
			chunkSize = defaultUploadChunkSize
		}
		s.ChunkSize = chunkSize
		return s
	}

	// makeReader builds a reader chain: optional rate limiter → optional progress tracker.
	makeReader := func() io.Reader {
		var r io.Reader = file
		if u.opts.MaxRate > 0 {
			r = &rateLimitedReader{reader: r, maxRate: u.opts.MaxRate, ctx: ctx}
		}
		if onProgress := uploadProgressFromContext(ctx); onProgress != nil {
			r = &progressReader{reader: r, onRead: onProgress}
		}
		return r
	}

	// stream is declared here so the resume-check block and the upload loop share
	// the same instance.  When resuming, the stream from the inner Sync is reused
	// directly, avoiding a redundant HEAD request.
	var stream *tusgo.UploadStream

	resumed := false

	if u.opts.NoResume {
		// Clear any stored state so this invocation and subsequent ones always
		// start fresh (until another interrupted upload creates a new state file).
		_ = clearUploadResumeLocations(resumeStatePath, legacyResumeStatePath)
	} else if location, statePathUsed, ok, err := loadUploadResumeLocationFromAny(resumeStatePath, legacyResumeStatePath); err != nil {
		return "", fmt.Errorf("load upload resume state: %w", err)
	} else if ok {
		upload.Location = location
		upload.RemoteSize = info.Size()
		stream = makeStream()
		if _, err := stream.Sync(); err == nil {
			if upload.RemoteSize > 0 && upload.RemoteSize != info.Size() {
				// Remote Upload-Length differs from local file size: the stored URL
				// belongs to a different version of the file.  Clear and restart.
				if clearErr := clearUploadResumeLocations(resumeStatePath, legacyResumeStatePath); clearErr != nil {
					return "", fmt.Errorf("clear stale upload resume state: %w", clearErr)
				}
				upload = tusgo.Upload{}
				stream = nil
			} else if upload.RemoteOffset >= info.Size() {
				// Server already has the complete file; nothing left to transfer.
				if clearErr := clearUploadResumeLocations(resumeStatePath, legacyResumeStatePath); clearErr != nil {
					return "", fmt.Errorf("clear completed upload resume state: %w", clearErr)
				}
				return upload.Location, nil
			} else {
				// Partial upload found; resume from the server's current offset.
				resumed = true
				if statePathUsed != resumeStatePath {
					if err := storeUploadResumeLocation(resumeStatePath, location); err != nil {
						return "", fmt.Errorf("migrate upload resume state: %w", err)
					}
					_ = clearUploadResumeLocation(statePathUsed)
				}
			}
		} else if isStaleTusUpload(err) {
			// The server rejected or could not find the upload (expired, deleted,
			// auth failure, or protocol violation).  Clear stale state and fall
			// through to create a fresh upload below.
			_ = clearUploadResumeLocations(resumeStatePath, legacyResumeStatePath)
			upload = tusgo.Upload{}
			stream = nil
		} else {
			// Network-level or context error: preserve state so the next invocation
			// can attempt to resume again once connectivity is restored.
			return "", fmt.Errorf("sync existing upload: %w", err)
		}
	}

	if !resumed {
		if _, err := client.CreateUpload(&upload, info.Size(), false, metadataWithChecksum); err != nil {
			return "", fmt.Errorf("create upload: %w", explainCreateUploadError(err, client.BaseURL.String(), info.Size(), client.Capabilities))
		}
		if err := storeUploadResumeLocation(resumeStatePath, upload.Location); err != nil {
			return "", fmt.Errorf("store upload resume state: %w", err)
		}
		// Sync the freshly created upload to confirm the server is at offset 0
		// before we start writing.
		stream = makeStream()
		if _, err := stream.Sync(); err != nil {
			return "", fmt.Errorf("sync upload: %w", err)
		}
	}

	// Notify the caller that we are resuming an interrupted upload and
	// pre-advance the progress bar to the server's confirmed offset so the
	// bar does not start at 0% on a resumed transfer.
	if resumed {
		offset := stream.Tell()
		if onProgress := uploadProgressFromContext(ctx); onProgress != nil {
			onProgress(offset)
		}
		msg := fmt.Sprintf("Resuming upload from %s...\n", formatSizeMB(offset))
		if fn := uploadPrintfFromContext(ctx); fn != nil {
			// TUI is running: route through program.Printf so the message
			// appears above the progress bar instead of being overwritten.
			fn("%s", msg)
		} else if w := uploadOutputFromContext(ctx); w != nil {
			fmt.Fprint(w, msg)
		}
	}

	// Seek the file to the server's confirmed offset.  For a resumed upload this
	// is the offset returned by the inner Sync above; for a fresh upload it is 0.
	if _, err := file.Seek(stream.Tell(), io.SeekStart); err != nil {
		return "", fmt.Errorf("seek file: %w", err)
	}

	reader := makeReader()

	for attempt := 0; ; attempt++ {
		if _, err := io.Copy(stream, reader); err == nil {
			break
		} else {
			if attempt >= maxResumeAttempts || !shouldResumeUpload(err) {
				return "", fmt.Errorf("upload data: %w", explainUploadTransferError(err, client.BaseURL.String(), info.Size(), client.Capabilities))
			}

			// Exponential backoff before retrying: 1 s → 2 s → 4 s (capped at 30 s).
			// The select respects context cancellation so the caller is not blocked
			// beyond the deadline.
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("upload cancelled during retry backoff: %w", ctx.Err())
			case <-time.After(backoff):
			}

			if _, syncErr := stream.Sync(); syncErr != nil {
				return "", fmt.Errorf("resync upload after transient error: %w (original error: %v)", syncErr, err)
			}
			if _, seekErr := file.Seek(stream.Tell(), io.SeekStart); seekErr != nil {
				return "", fmt.Errorf("seek file after transient upload error: %w", seekErr)
			}

			// Recreate stream to discard any dirty chunk buffer and start cleanly
			// from the synced offset.
			stream = makeStream()
			reader = makeReader()
		}
	}

	if err := clearUploadResumeLocations(resumeStatePath, legacyResumeStatePath); err != nil {
		return "", fmt.Errorf("clear upload resume state: %w", err)
	}

	return upload.Location, nil
}

type progressReader struct {
	reader io.Reader
	onRead func(int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && r.onRead != nil {
		r.onRead(int64(n))
	}
	return n, err
}

// rateLimitedReader wraps a reader and throttles throughput to maxRate bytes/sec.
//
// Sleep debt is accumulated across reads and only a single timer is allocated
// and reused for the lifetime of the reader, avoiding per-read allocations at
// high chunk rates (e.g. 64 MiB chunks → ~1,600 reads for a 100 GiB file).
// Context cancellation is honoured during each sleep interval.
type rateLimitedReader struct {
	reader  io.Reader
	maxRate int64 // bytes per second; 0 means unlimited
	ctx     context.Context
	debt    time.Duration // accumulated un-slept time
	timer   *time.Timer   // reused across reads; nil until first sleep
}

// minRateLimitSleep is the minimum accumulated debt before we actually sleep.
// Keeping this above OS timer resolution (~1 ms on most platforms) avoids
// spinning on tiny sleeps for high-rate limits with small reads.
const minRateLimitSleep = 5 * time.Millisecond

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := r.reader.Read(p)
	if n > 0 && r.maxRate > 0 {
		// How long should this many bytes have taken at the target rate?
		expected := time.Duration(float64(n) / float64(r.maxRate) * float64(time.Second))
		// Accumulate positive debt; clamp to zero when we're already slow
		// (negative debt) so we never "speed up" to compensate for a slow read.
		if elapsed := time.Since(start); elapsed < expected {
			r.debt += expected - elapsed
		}
		if r.debt >= minRateLimitSleep {
			sleep := r.debt
			r.debt = 0
			if r.timer == nil {
				r.timer = time.NewTimer(sleep)
			} else {
				r.timer.Reset(sleep)
			}
			select {
			case <-r.ctx.Done():
				// Stop the timer and drain its channel so the next Reset is safe.
				if !r.timer.Stop() {
					select {
					case <-r.timer.C:
					default:
					}
				}
				return n, r.ctx.Err()
			case <-r.timer.C:
				// Timer fired normally; channel is now empty — Reset is safe.
			}
		}
	}
	return n, err
}

func shouldResumeUpload(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	if errors.Is(err, tusgo.ErrCannotUpload) ||
		errors.Is(err, tusgo.ErrUploadTooLarge) ||
		errors.Is(err, tusgo.ErrUploadDoesNotExist) ||
		errors.Is(err, tusgo.ErrChecksumMismatch) ||
		errors.Is(err, tusgo.ErrProtocol) ||
		errors.Is(err, tusgo.ErrUnexpectedResponse) {
		// ErrUnexpectedResponse covers auth failures (401/403) and other definitive
		// server rejections that will not resolve on an immediate retry.
		return false
	}

	return true
}

// isStaleTusUpload returns true for errors that indicate the stored resume URL is
// no longer usable (upload not found, expired, access denied, or a protocol
// violation).  In these cases the caller should clear the resume state and
// create a fresh upload rather than returning an unrecoverable error.
func isStaleTusUpload(err error) bool {
	return errors.Is(err, tusgo.ErrUploadDoesNotExist) ||
		errors.Is(err, tusgo.ErrUnexpectedResponse) ||
		errors.Is(err, tusgo.ErrCannotUpload) ||
		errors.Is(err, tusgo.ErrProtocol)
}

func copyMetadata(metadata map[string]string) map[string]string {
	result := make(map[string]string, len(metadata)+1)
	for k, v := range metadata {
		result[k] = v
	}
	return result
}

// sha256Hex hashes r and returns the hex-encoded SHA-256 digest.  ctx is
// checked between 32 KiB reads so that a cancellation or deadline propagates
// without waiting for the entire stream to finish.
func sha256Hex(ctx context.Context, r io.Reader) (string, error) {
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := r.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func explainTusUnexpectedResponse(err error, endpoint string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, tusgo.ErrUnexpectedResponse) {
		return fmt.Errorf("unexpected response from tus endpoint %q; ensure the endpoint points to a tus upload root (often requires a trailing slash) and provide a valid --upload-token/ABC_UPLOAD_TOKEN", endpoint)
	}
	return err
}

func explainCreateUploadError(err error, endpoint string, fileSize int64, capabilities *tusgo.ServerCapabilities) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, tusgo.ErrUploadTooLarge) {
		if capabilities != nil && capabilities.MaxSize > 0 {
			return fmt.Errorf("file is too large for tus endpoint %q: file size is %d bytes, Tus-Max-Size is %d bytes", endpoint, fileSize, capabilities.MaxSize)
		}
		return fmt.Errorf("file is too large for tus endpoint %q: file size is %d bytes; reduce file size or increase Tus-Max-Size on the server", endpoint, fileSize)
	}
	return explainTusUnexpectedResponse(err, endpoint)
}

func explainUploadTransferError(err error, endpoint string, fileSize int64, capabilities *tusgo.ServerCapabilities) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, tusgo.ErrUploadTooLarge) {
		if capabilities != nil && capabilities.MaxSize > 0 {
			return fmt.Errorf("upload exceeds tus limit at %q after transfer started: file size is %d bytes, Tus-Max-Size is %d bytes", endpoint, fileSize, capabilities.MaxSize)
		}
		return fmt.Errorf("upload exceeds tus limit at %q after transfer started: file size is %d bytes", endpoint, fileSize)
	}
	return explainTusUnexpectedResponse(err, endpoint)
}

func uploadResumeStatePath(endpoint, filePath string, size int64, modifiedAtUnixNano int64) (string, error) {
	primary, _, err := uploadResumeStatePaths(endpoint, filePath, size, modifiedAtUnixNano)
	return primary, err
}

func uploadResumeStatePaths(endpoint, filePath string, size int64, modifiedAtUnixNano int64) (string, string, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", "", err
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", "", err
	}
	primaryKey := fmt.Sprintf("%s\n%s", endpoint, absPath)
	primaryHash := sha256.Sum256([]byte(primaryKey))
	primaryName := hex.EncodeToString(primaryHash[:]) + ".url"

	legacyKey := fmt.Sprintf("%s\n%s\n%d\n%d", endpoint, absPath, size, modifiedAtUnixNano)
	legacyHash := sha256.Sum256([]byte(legacyKey))
	legacyName := hex.EncodeToString(legacyHash[:]) + ".url"

	baseDir := filepath.Join(cacheDir, resumeStateSubpath)
	return filepath.Join(baseDir, primaryName), filepath.Join(baseDir, legacyName), nil
}

func loadUploadResumeLocationFromAny(statePaths ...string) (string, string, bool, error) {
	for _, statePath := range statePaths {
		if strings.TrimSpace(statePath) == "" {
			continue
		}
		location, ok, err := loadUploadResumeLocation(statePath)
		if err != nil {
			return "", "", false, err
		}
		if ok {
			return location, statePath, true, nil
		}
	}
	return "", "", false, nil
}

func loadUploadResumeLocation(statePath string) (string, bool, error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	location := strings.TrimSpace(string(raw))
	if location == "" {
		return "", false, nil
	}
	return location, true, nil
}

func storeUploadResumeLocation(statePath, location string) error {
	if strings.TrimSpace(location) == "" {
		return fmt.Errorf("upload location is empty")
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		return err
	}
	return os.WriteFile(statePath, []byte(location+"\n"), 0600)
}

func clearUploadResumeLocation(statePath string) error {
	err := os.Remove(statePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func clearUploadResumeLocations(statePaths ...string) error {
	for _, statePath := range statePaths {
		if strings.TrimSpace(statePath) == "" {
			continue
		}
		if err := clearUploadResumeLocation(statePath); err != nil {
			return err
		}
	}
	return nil
}

// parseByteCount parses a human-readable byte count such as "64MB", "2MiB", "1GB".
// Supported suffixes (case-insensitive): K/KB/KiB, M/MB/MiB, G/GB/GiB.
// The iB variants use powers of 1024; all others use powers of 1000.
func parseByteCount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}

	upper := strings.ToUpper(s)
	var multiplier int64 = 1
	rest := s

	switch {
	case strings.HasSuffix(upper, "GIB"):
		multiplier = 1024 * 1024 * 1024
		rest = s[:len(s)-3]
	case strings.HasSuffix(upper, "MIB"):
		multiplier = 1024 * 1024
		rest = s[:len(s)-3]
	case strings.HasSuffix(upper, "KIB"):
		multiplier = 1024
		rest = s[:len(s)-3]
	case strings.HasSuffix(upper, "GB"):
		multiplier = 1000 * 1000 * 1000
		rest = s[:len(s)-2]
	case strings.HasSuffix(upper, "MB"):
		multiplier = 1000 * 1000
		rest = s[:len(s)-2]
	case strings.HasSuffix(upper, "KB"):
		multiplier = 1000
		rest = s[:len(s)-2]
	case strings.HasSuffix(upper, "G"):
		multiplier = 1000 * 1000 * 1000
		rest = s[:len(s)-1]
	case strings.HasSuffix(upper, "M"):
		multiplier = 1000 * 1000
		rest = s[:len(s)-1]
	case strings.HasSuffix(upper, "K"):
		multiplier = 1000
		rest = s[:len(s)-1]
	}

	n, parseErr := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
	if parseErr != nil {
		return 0, fmt.Errorf("invalid byte count %q: %w", s, parseErr)
	}
	if n <= 0 {
		return 0, fmt.Errorf("byte count must be positive, got %d", n)
	}
	return n * multiplier, nil
}

// parseMaxRate parses a rate string such as "50MB/s" or "10MiB/s".
// The "/s" suffix is stripped before delegating to parseByteCount.
func parseMaxRate(s string) (int64, error) {
	trimmed := strings.TrimSpace(s)
	if strings.HasSuffix(strings.ToLower(trimmed), "/s") {
		trimmed = trimmed[:len(trimmed)-2]
	}
	return parseByteCount(trimmed)
}

// parseMetaFlags converts a slice of "key=value" strings into a map.
// Built-in metadata keys set by the uploader always take precedence over values
// provided here.
func parseMetaFlags(meta []string) (map[string]string, error) {
	result := make(map[string]string, len(meta))
	for _, kv := range meta {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			return nil, fmt.Errorf("metadata entry %q must be in key=value format", kv)
		}
		k := strings.TrimSpace(kv[:idx])
		v := kv[idx+1:]
		if k == "" {
			return nil, fmt.Errorf("metadata key must not be empty in %q", kv)
		}
		result[k] = v
	}
	return result, nil
}

// uploadResumeStatePrimaryPath returns the primary resume state file path for a
// given endpoint and local file path.  Unlike uploadResumeStatePaths, this helper
// does not require the file's size or modification time, making it suitable for
// status/clear operations that may be invoked after the file has been moved or deleted.
func uploadResumeStatePrimaryPath(endpoint, filePath string) (string, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("%s\n%s", endpoint, absPath)
	hash := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(hash[:]) + ".url"
	return filepath.Join(cacheDir, resumeStateSubpath, name), nil
}
