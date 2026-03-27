package data

import (
	"context"

	"github.com/spf13/cobra"
)

// UploaderOptions configures optional behaviour of the tus uploader.
type UploaderOptions struct {
	ChunkSize int64 // 0 = tusgo default (~2 MiB)
	MaxRate   int64 // bytes/sec; 0 = unlimited
	NoResume  bool  // always start a fresh upload, ignoring stored state
}

// Uploader uploads files to a tus endpoint.
type Uploader interface {
	Upload(ctx context.Context, filePath string, metadata map[string]string) (string, error)
}

// ClientFactory creates an Uploader from connection parameters and options.
type ClientFactory func(endpoint, accessToken string, opts UploaderOptions) (Uploader, error)

// defaultClientFactory creates a real tus uploader.
func defaultClientFactory(endpoint, accessToken string, opts UploaderOptions) (Uploader, error) {
	return newTusUploader(endpoint, accessToken, opts)
}

// NewCmd returns the "data" subcommand group.
// serverURL, accessToken, and workspace are pointers to the root command's persistent flags
// so that they are evaluated after flag parsing.
// If factory is nil, the default uploader factory is used.
func NewCmd(serverURL, accessToken, workspace *string, factory ...ClientFactory) *cobra.Command {
	f := defaultClientFactory
	if len(factory) > 0 && factory[0] != nil {
		f = factory[0]
	}

	cmd := &cobra.Command{
		Use:   "data",
		Short: "Manage data",
		Long:  `Commands for uploading and managing data on the abc-cluster platform.`,
	}
	cmd.AddCommand(newUploadCmd(serverURL, accessToken, workspace, f))
	cmd.AddCommand(newEncryptCmd())
	cmd.AddCommand(newDecryptCmd())
	return cmd
}
