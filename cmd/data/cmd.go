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
		Long: `Commands for uploading, downloading, and moving data on the abc-cluster platform.

Common workflows:

  Upload a file to cluster MinIO storage:
    abc data upload ./genome.fa

  Upload and immediately stage into your JupyterLab workbench:
    abc data upload ./genome.fa --workbench

  Fetch data from the internet into cluster MinIO (server-side Nomad job):
    abc data fetch https://example.com/data.tar.gz

  Download a file from MinIO to your local machine (resumable):
    abc data pull s3://su-mbhg-hostgen/user/calm-dassie/data/results.csv

  Stage a MinIO file into your workbench ~/data/ directory:
    abc data stage s3://su-mbhg-hostgen/user/calm-dassie/data/genome.fa

  Browse your MinIO bucket:
    abc data ls

Advanced / power-user commands:
  abc data download   full-featured download: choose tool, driver, destination
  abc data copy       server-side S3 copy
  abc data move       server-side S3 move`,
	}
	cmd.AddCommand(newUploadCmd(serverURL, accessToken, workspace, f))
	cmd.AddCommand(newUploadsCmd())
	cmd.AddCommand(newEncryptCmd())
	cmd.AddCommand(newDecryptCmd())
	// Focused data movement commands (easier to use than abc data download).
	cmd.AddCommand(newFetchCmd(serverURL, accessToken, workspace))
	cmd.AddCommand(newPullCmd())
	cmd.AddCommand(newStageCmd())
	// Power-user / backwards-compatible command with full flag surface.
	cmd.AddCommand(newDownloadCmd(serverURL, accessToken, workspace, PipelineFactory))
	cmd.AddCommand(newCopyCmd(serverURL, accessToken, workspace))
	cmd.AddCommand(newMoveCmd(serverURL, accessToken, workspace))
	cmd.AddCommand(newLsCmd())
	cmd.AddCommand(newStatCmd())
	return cmd
}
