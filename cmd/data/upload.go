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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/bdragon300/tusgo"
	"github.com/spf13/cobra"
)

const (
	bytesPerMB         = 1024 * 1024
	resumeStateSubpath = "abc-cluster-cli/tus-resume"
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
}

type uploadProgressContextKey struct{}

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
			return runUpload(cmd, opts, *serverURL, *accessToken, *workspace, factory)
		},
	}

	cmd.Flags().StringVar(&opts.name, "name", "", "display name for the uploaded file")
	cmd.Flags().StringVar(&opts.endpoint, "endpoint", os.Getenv("ABC_UPLOAD_ENDPOINT"), "tus upload endpoint URL (or set ABC_UPLOAD_ENDPOINT; defaults to <url>/data/uploads)")
	cmd.Flags().StringVar(&opts.cryptPassword, "crypt-password", "", "rclone crypt password for client-side encryption")
	cmd.Flags().StringVar(&opts.cryptSalt, "crypt-salt", "", "rclone crypt salt (password2) for client-side encryption")
	cmd.Flags().StringVar(&opts.token, "upload-token", os.Getenv("ABC_UPLOAD_TOKEN"), "bearer token for tus uploads (or set ABC_UPLOAD_TOKEN); falls back to --access-token")
	cmd.Flags().BoolVar(&opts.checksum, "checksum", true, "include sha256 checksum metadata in tus upload metadata")
	cmd.Flags().BoolVar(&opts.progress, "progress", true, "show live progress bars for encryption and uploads")
	cmd.Flags().BoolVar(&opts.parallel, "parallel", true, "upload directory files in parallel")
	cmd.Flags().IntVar(&opts.parallelJobs, "parallel-jobs", runtime.NumCPU(), "number of parallel upload workers when --parallel=true")

	return cmd
}

func runUpload(cmd *cobra.Command, opts *uploadOptions, serverURL, accessToken, workspace string, factory ClientFactory) error {
	info, err := os.Stat(opts.filePath)
	if err != nil {
		return fmt.Errorf("failed to access path %q: %w", opts.filePath, err)
	}
	if info.IsDir() {
		if opts.name != "" {
			return fmt.Errorf("--name can only be used when uploading a single file")
		}
	}

	endpoint, err := resolveEndpoint(opts.endpoint, serverURL, workspace)
	if err != nil {
		return err
	}

	cryptor, err := uploadCryptConfig(opts.cryptPassword, opts.cryptSalt)
	if err != nil {
		return err
	}

	authToken := strings.TrimSpace(opts.token)
	if authToken == "" {
		authToken = accessToken
	}

	uploader, err := factory(endpoint, authToken)
	if err != nil {
		return fmt.Errorf("failed to initialize upload client: %w", err)
	}

	if info.IsDir() {
		jobs := opts.parallelJobs
		if !opts.parallel {
			jobs = 1
		}
		if jobs < 1 {
			return fmt.Errorf("parallel-jobs must be >= 1")
		}
		return uploadDirectory(cmd, uploader, opts.filePath, cryptor, opts.checksum, opts.progress, jobs)
	}

	return uploadSingleFile(cmd, uploader, opts.filePath, opts.name, info.Size(), cryptor, opts.checksum, opts.progress)
}

func resolveEndpoint(endpoint, serverURL, workspace string) (string, error) {
	if endpoint == "" {
		return buildDefaultEndpoint(serverURL, workspace)
	}
	return applyWorkspace(normalizeTusEndpoint(endpoint), workspace)
}

func buildDefaultEndpoint(serverURL, workspace string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("invalid server URL %q: %w", serverURL, err)
	}
	trimmedPath := strings.Trim(parsed.Path, "/")
	if trimmedPath == "" {
		parsed.Path = "/data/uploads/"
	} else {
		parsed.Path = "/" + trimmedPath + "/data/uploads/"
	}
	return applyWorkspace(parsed.String(), workspace)
}

func normalizeTusEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	} else if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	return parsed.String()
}

func applyWorkspace(endpoint, workspace string) (string, error) {
	if workspace == "" {
		return endpoint, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid upload endpoint %q: %w", endpoint, err)
	}
	q := parsed.Query()
	if existing := q.Get("workspaceId"); existing != "" {
		if existing != workspace {
			return "", fmt.Errorf("upload endpoint workspaceId %q does not match requested workspace %q", existing, workspace)
		}
		return parsed.String(), nil
	}
	q.Set("workspaceId", workspace)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

type uploadResult struct {
	relPath  string
	sizeMB   string
	checksum string
	err      error
}

func uploadDirectory(cmd *cobra.Command, uploader Uploader, dir string, cryptor *cryptConfig, checksumEnabled bool, progressEnabled bool, jobs int) error {
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
			result := uploadDirectoryFile(cmd.Context(), cmd.OutOrStdout(), uploader, dir, file, cryptor, checksumEnabled, progressEnabled)
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
				result := uploadDirectoryFile(ctx, cmd.OutOrStdout(), uploader, dir, file, cryptor, checksumEnabled, false)
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

func uploadDirectoryFile(ctx context.Context, out io.Writer, uploader Uploader, dir string, file uploadFile, cryptor *cryptConfig, checksumEnabled bool, progressEnabled bool) uploadResult {
	result := uploadResult{}
	relPath, err := filepath.Rel(dir, file.path)
	if err != nil {
		result.err = fmt.Errorf("failed to resolve path for %q: %w", file.path, err)
		return result
	}
	result.relPath = relPath

	metadata := map[string]string{
		"filename":     filepath.Base(file.path),
		"relativePath": filepath.ToSlash(relPath),
	}
	encryptProgress := newProgressReporter(out, progressEnabled && cryptor != nil, fmt.Sprintf("Encrypting %s", relPath), file.size)
	uploadPath, cleanup, err := encryptForUpload(file.path, cryptor, func(n int64) {
		encryptProgress.Add(n)
	})
	encryptDoneErr := encryptProgress.Complete()
	if encryptDoneErr != nil {
		result.err = fmt.Errorf("failed to render encryption progress: %w", encryptDoneErr)
		return result
	}
	if err != nil {
		result.err = fmt.Errorf("data encryption failed for %q: %w", relPath, err)
		return result
	}
	if !checksumEnabled {
		metadata["checksum"] = ""
	}
	uploadInfo, err := os.Stat(uploadPath)
	if err != nil {
		result.err = fmt.Errorf("failed to access upload file for %q: %w", relPath, err)
		return result
	}
	if checksumEnabled {
		checksumValue, err := fileSHA256(uploadPath)
		if err != nil {
			result.err = fmt.Errorf("failed to compute checksum for %q: %w", relPath, err)
			return result
		}
		result.checksum = "sha256:" + checksumValue
		metadata["checksum"] = result.checksum
	}
	uploadProgress := newProgressReporter(out, progressEnabled, fmt.Sprintf("Uploading %s", relPath), uploadInfo.Size())
	_, err = uploader.Upload(withUploadProgress(ctx, func(n int64) {
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
		result.err = fmt.Errorf("data upload failed for %q: %w", relPath, err)
		return result
	}

	result.sizeMB = formatSizeMB(uploadInfo.Size())
	return result
}

func uploadSingleFile(cmd *cobra.Command, uploader Uploader, filePath, name string, size int64, cryptor *cryptConfig, checksumEnabled bool, progressEnabled bool) error {
	metadata := map[string]string{
		"filename": filepath.Base(filePath),
	}
	if name != "" {
		metadata["name"] = name
	}
	if !checksumEnabled {
		metadata["checksum"] = ""
	}

	encryptProgress := newProgressReporter(cmd.OutOrStdout(), progressEnabled && cryptor != nil, fmt.Sprintf("Encrypting %s", filepath.Base(filePath)), size)
	uploadPath, cleanup, err := encryptForUpload(filePath, cryptor, func(n int64) {
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
		checksumValue, err := fileSHA256(uploadPath)
		if err != nil {
			return fmt.Errorf("failed to compute checksum: %w", err)
		}
		localChecksum = "sha256:" + checksumValue
		metadata["checksum"] = localChecksum
	}
	uploadProgress := newProgressReporter(cmd.OutOrStdout(), progressEnabled, fmt.Sprintf("Uploading %s", filepath.Base(filePath)), uploadInfo.Size())
	_, err = uploader.Upload(withUploadProgress(cmd.Context(), func(n int64) {
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
		return fmt.Errorf("data upload failed: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "File uploaded successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Size: %s\n", formatSizeMB(uploadInfo.Size()))
	if checksumEnabled {
		fmt.Fprintf(cmd.OutOrStdout(), "  Checksum: %s\n", localChecksum)
	}

	return nil
}

func fileSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return sha256Hex(file)
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
	client *tusgo.Client
}

func newTusUploader(endpoint, accessToken string) (Uploader, error) {
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
	return &tusUploader{client: client}, nil
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
		checksumValue, err := sha256Hex(file)
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
	resumeStatePath, resumePathErr := uploadResumeStatePath(client.BaseURL.String(), filePath, info.Size(), info.ModTime().UnixNano())
	if resumePathErr != nil {
		return "", fmt.Errorf("build upload resume state path: %w", resumePathErr)
	}

	resumed := false
	if location, ok, err := loadUploadResumeLocation(resumeStatePath); err != nil {
		return "", fmt.Errorf("load upload resume state: %w", err)
	} else if ok {
		upload.Location = location
		upload.RemoteSize = info.Size()
		stream := tusgo.NewUploadStream(client, &upload)
		if _, err := stream.Sync(); err == nil {
			resumed = true
		} else if errors.Is(err, tusgo.ErrUploadDoesNotExist) {
			_ = clearUploadResumeLocation(resumeStatePath)
			upload = tusgo.Upload{}
		} else {
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
	}

	stream := tusgo.NewUploadStream(client, &upload)
	if _, err := stream.Sync(); err != nil {
		return "", fmt.Errorf("sync upload: %w", err)
	}
	if _, err := file.Seek(stream.Tell(), io.SeekStart); err != nil {
		return "", fmt.Errorf("seek file: %w", err)
	}

	reader := io.Reader(file)
	if onProgress := uploadProgressFromContext(ctx); onProgress != nil {
		reader = &progressReader{reader: file, onRead: onProgress}
	}

	for attempt := 0; ; attempt++ {
		if _, err := io.Copy(stream, reader); err == nil {
			break
		} else {
			if attempt >= maxResumeAttempts || !shouldResumeUpload(err) {
				return "", fmt.Errorf("upload data: %w", explainUploadTransferError(err, client.BaseURL.String(), info.Size(), client.Capabilities))
			}
			if _, syncErr := stream.Sync(); syncErr != nil {
				return "", fmt.Errorf("resync upload after transient error: %w (original error: %v)", syncErr, err)
			}
			if _, seekErr := file.Seek(stream.Tell(), io.SeekStart); seekErr != nil {
				return "", fmt.Errorf("seek file after transient upload error: %w", seekErr)
			}

			// Recreate stream to clear any dirty chunk buffer before retrying from synced offset.
			stream = tusgo.NewUploadStream(client, &upload)
			if onProgress := uploadProgressFromContext(ctx); onProgress != nil {
				reader = &progressReader{reader: file, onRead: onProgress}
			} else {
				reader = file
			}
		}
	}

	if err := clearUploadResumeLocation(resumeStatePath); err != nil {
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
		errors.Is(err, tusgo.ErrProtocol) {
		return false
	}

	return true
}

func copyMetadata(metadata map[string]string) map[string]string {
	result := make(map[string]string, len(metadata)+1)
	for k, v := range metadata {
		result[k] = v
	}
	return result
}

func sha256Hex(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
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
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("%s\n%s\n%d\n%d", endpoint, absPath, size, modifiedAtUnixNano)
	hash := sha256.Sum256([]byte(key))
	fileName := hex.EncodeToString(hash[:]) + ".url"
	return filepath.Join(cacheDir, resumeStateSubpath, fileName), nil
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
