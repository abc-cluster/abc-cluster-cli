package data

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bdragon300/tusgo"
	"github.com/spf13/cobra"
)

type uploadOptions struct {
	filePath      string
	name          string
	endpoint      string
	cryptPassword string
	cryptSalt     string
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
	cmd.Flags().StringVar(&opts.endpoint, "endpoint", "", "tus upload endpoint URL (defaults to <url>/data/uploads)")
	cmd.Flags().StringVar(&opts.cryptPassword, "crypt-password", "", "rclone crypt password for client-side encryption")
	cmd.Flags().StringVar(&opts.cryptSalt, "crypt-salt", "", "rclone crypt salt (password2) for client-side encryption")

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

	uploader, err := factory(endpoint, accessToken)
	if err != nil {
		return fmt.Errorf("failed to initialize upload client: %w", err)
	}

	if info.IsDir() {
		return uploadDirectory(cmd, uploader, opts.filePath, cryptor)
	}

	return uploadSingleFile(cmd, uploader, opts.filePath, opts.name, info.Size(), cryptor)
}

func resolveEndpoint(endpoint, serverURL, workspace string) (string, error) {
	if endpoint == "" {
		return buildDefaultEndpoint(serverURL, workspace)
	}
	return applyWorkspace(endpoint, workspace)
}

func buildDefaultEndpoint(serverURL, workspace string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("invalid server URL %q: %w", serverURL, err)
	}
	trimmedPath := strings.Trim(parsed.Path, "/")
	if trimmedPath == "" {
		parsed.Path = "/data/uploads"
	} else {
		parsed.Path = "/" + trimmedPath + "/data/uploads"
	}
	return applyWorkspace(parsed.String(), workspace)
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

func uploadDirectory(cmd *cobra.Command, uploader Uploader, dir string, cryptor *cryptConfig) error {
	files, err := collectFiles(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no files found in directory %q", dir)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Uploading %d files...\n", len(files))
	for _, file := range files {
		relPath, err := filepath.Rel(dir, file.path)
		if err != nil {
			return fmt.Errorf("failed to resolve path for %q: %w", file.path, err)
		}

		metadata := map[string]string{
			"filename":     filepath.Base(file.path),
			"relativePath": filepath.ToSlash(relPath),
		}
		uploadPath, cleanup, err := encryptForUpload(file.path, cryptor)
		if err != nil {
			return fmt.Errorf("data encryption failed for %q: %w", relPath, err)
		}
		location, err := uploader.Upload(cmd.Context(), uploadPath, metadata)
		if cleanup != nil {
			if cleanupErr := cleanup(); cleanupErr != nil {
				return fmt.Errorf("failed to clean up encrypted file for %q: %w", relPath, cleanupErr)
			}
		}
		if err != nil {
			return fmt.Errorf("data upload failed for %q: %w", relPath, err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Uploaded %s\n", relPath)
		fmt.Fprintf(cmd.OutOrStdout(), "  Location: %s\n", location)
		fmt.Fprintf(cmd.OutOrStdout(), "  Size: %d bytes\n", file.size)
	}

	return nil
}

func uploadSingleFile(cmd *cobra.Command, uploader Uploader, filePath, name string, size int64, cryptor *cryptConfig) error {
	metadata := map[string]string{
		"filename": filepath.Base(filePath),
	}
	if name != "" {
		metadata["name"] = name
	}

	uploadPath, cleanup, err := encryptForUpload(filePath, cryptor)
	if err != nil {
		return fmt.Errorf("data encryption failed: %w", err)
	}
	location, err := uploader.Upload(cmd.Context(), uploadPath, metadata)
	if cleanup != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return fmt.Errorf("failed to clean up encrypted file: %w", cleanupErr)
		}
	}
	if err != nil {
		return fmt.Errorf("data upload failed: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "File uploaded successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Location: %s\n", location)
	fmt.Fprintf(cmd.OutOrStdout(), "  Size: %d bytes\n", size)

	return nil
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
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}

	client := u.client.WithContext(ctx)

	upload := tusgo.Upload{}
	if _, err := client.CreateUpload(&upload, info.Size(), false, metadata); err != nil {
		return "", fmt.Errorf("create upload: %w", err)
	}

	stream := tusgo.NewUploadStream(client, &upload)
	if _, err := stream.Sync(); err != nil {
		return "", fmt.Errorf("sync upload: %w", err)
	}
	if _, err := file.Seek(stream.Tell(), io.SeekStart); err != nil {
		return "", fmt.Errorf("seek file: %w", err)
	}

	if _, err := io.Copy(stream, file); err != nil {
		return "", fmt.Errorf("upload data: %w", err)
	}

	return upload.Location, nil
}
