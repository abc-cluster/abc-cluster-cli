package pipeline

import (
"github.com/abc-cluster/abc-cluster-cli/api"
"github.com/spf13/cobra"
)

// PipelineRunner is the interface for submitting pipeline runs.
type PipelineRunner interface {
SubmitPipelineRun(req *api.PipelineRunRequest) (*api.PipelineRunResponse, error)
}

// ClientFactory creates a PipelineRunner from connection parameters.
type ClientFactory func(serverURL, accessToken, workspace string) PipelineRunner

// defaultClientFactory creates a real API client.
func defaultClientFactory(serverURL, accessToken, workspace string) PipelineRunner {
return api.NewClient(serverURL, accessToken, workspace)
}

// NewCmd returns the "pipeline" subcommand group.
// serverURL, accessToken, and workspace are pointers to the root command's persistent flags
// so that they are evaluated after flag parsing.
// If factory is nil, the default API client factory is used.
func NewCmd(serverURL, accessToken, workspace *string, factory ...ClientFactory) *cobra.Command {
f := defaultClientFactory
if len(factory) > 0 && factory[0] != nil {
f = factory[0]
}

cmd := &cobra.Command{
Use:   "pipeline",
Short: "Manage pipelines",
Long:  `Commands for managing and running pipelines on the abc-cluster platform.`,
}
cmd.AddCommand(newRunCmd(serverURL, accessToken, workspace, f))
return cmd
}
