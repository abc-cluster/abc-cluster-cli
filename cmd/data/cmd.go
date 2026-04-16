package data

import (
	"context"

	"github.com/abc-cluster/abc-cluster-cli/api"
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

// PipelineRunner is the interface for submitting a pipeline run.
type PipelineRunner interface {
	SubmitPipelineRun(req *api.PipelineRunRequest) (*api.PipelineRunResponse, error)
}

// PipelineClientFactory creates a PipelineRunner from connection parameters.
type PipelineClientFactory func(serverURL, accessToken, workspace string) PipelineRunner

// defaultPipelineClientFactory creates a real API client.
func defaultPipelineClientFactory(serverURL, accessToken, workspace string) PipelineRunner {
	return api.NewClient(serverURL, accessToken, workspace)
}

// PipelineFactory is used by data download command and can be replaced in tests.
var PipelineFactory = defaultPipelineClientFactory

// NewCmd returns the "data" subcommand group.
// serverURL, accessToken, and workspace are pointers to the root command's persistent flags
// so that they are evaluated after flag parsing.
// If factory is nil, the default uploader factory is used.
func NewCmd(serverURL, accessToken, workspace *string, dataFactory ...ClientFactory) *cobra.Command {
	f := defaultClientFactory
	if len(dataFactory) > 0 && dataFactory[0] != nil {
		f = dataFactory[0]
	}

	cmd := &cobra.Command{
		Use:   "data",
		Short: "Manage data",
		Long:  `Commands for uploading and managing data on the abc-cluster platform.`,
	}
	cmd.AddCommand(newUploadCmd(serverURL, accessToken, workspace, f))
	cmd.AddCommand(newEncryptCmd())
	cmd.AddCommand(newDecryptCmd())
	cmd.AddCommand(newDownloadCmd(serverURL, accessToken, workspace, PipelineFactory))
	cmd.AddCommand(newCopyCmd(serverURL, accessToken, workspace))
	cmd.AddCommand(newMoveCmd(serverURL, accessToken, workspace))
	return cmd
}
