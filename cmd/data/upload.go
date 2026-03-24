package data

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"

	"github.com/bdragon300/tusgo"
	"github.com/spf13/cobra"
)

type uploadOptions struct {
	filePath string
	name     string
	endpoint string
}

// newUploadCmd returns the "data upload" subcommand.
func newUploadCmd(serverURL, accessToken, workspace *string, factory ClientFactory) *cobra.Command {
	opts := &uploadOptions{}

	cmd := &cobra.Command{
		Use:   "upload <file>",
		Short: "Upload a file using tus",
		Long: `Upload a file to the abc-cluster data service using the tus resumable upload protocol.

Examples:
  # Upload a local file
  abc data upload ./data.csv

  # Upload with a display name
  abc data upload ./data.csv --name sample-data`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.filePath = args[0]
			return runUpload(cmd, opts, *serverURL, *accessToken, *workspace, factory)
		},
	}

	cmd.Flags().StringVar(&opts.name, "name", "", "display name for the uploaded file")
	cmd.Flags().StringVar(&opts.endpoint, "endpoint", "", "tus upload endpoint URL (defaults to <url>/data/uploads)")

	return cmd
}

func runUpload(cmd *cobra.Command, opts *uploadOptions, serverURL, accessToken, workspace string, factory ClientFactory) error {
	info, err := os.Stat(opts.filePath)
	if err != nil {
		return fmt.Errorf("failed to access file %q: %w", opts.filePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("path %q is a directory", opts.filePath)
	}

	endpoint, err := resolveEndpoint(opts.endpoint, serverURL, workspace)
	if err != nil {
		return err
	}

	uploader, err := factory(endpoint, accessToken)
	if err != nil {
		return fmt.Errorf("failed to initialize upload client: %w", err)
	}

	metadata := map[string]string{
		"filename": filepath.Base(opts.filePath),
	}
	if opts.name != "" {
		metadata["name"] = opts.name
	}

	location, err := uploader.Upload(cmd.Context(), opts.filePath, metadata)
	if err != nil {
		return fmt.Errorf("data upload failed: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "File uploaded successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Location: %s\n", location)
	fmt.Fprintf(cmd.OutOrStdout(), "  Size: %d bytes\n", info.Size())

	return nil
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
	parsed.Path = path.Join("/", parsed.Path, "data", "uploads")
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
	if q.Get("workspaceId") == "" {
		q.Set("workspaceId", workspace)
		parsed.RawQuery = q.Encode()
	}
	return parsed.String(), nil
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
		client.GetRequest = func(method, url string, body io.Reader, _ *tusgo.Client, _ *http.Client) (*http.Request, error) {
			req, err := http.NewRequest(method, url, body)
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

	client := u.client
	if ctx != nil {
		client = client.WithContext(ctx)
	}

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
