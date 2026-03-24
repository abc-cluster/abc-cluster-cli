package data

import (
	"context"

	"github.com/spf13/cobra"
)

// Uploader uploads files to a tus endpoint.
type Uploader interface {
	Upload(ctx context.Context, filePath string, metadata map[string]string) (string, error)
}

// ClientFactory creates an Uploader from connection parameters.
type ClientFactory func(endpoint, accessToken string) (Uploader, error)

// defaultClientFactory creates a real tus uploader.
func defaultClientFactory(endpoint, accessToken string) (Uploader, error) {
	return newTusUploader(endpoint, accessToken)
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
	return cmd
}
