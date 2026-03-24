package pipeline

import (
"encoding/json"
"fmt"
"os"

"github.com/abc-cluster/abc-cluster-cli/api"
"github.com/spf13/cobra"
"gopkg.in/yaml.v3"
)

// runOptions holds the flags for the "pipeline run" command.
type runOptions struct {
pipeline   string
runName    string
revision   string
profile    string
workDir    string
configFile string
paramsFile string
}

// newRunCmd returns the "pipeline run" subcommand.
func newRunCmd(serverURL, accessToken, workspace *string, factory ClientFactory) *cobra.Command {
opts := &runOptions{}

cmd := &cobra.Command{
Use:   "run",
Short: "Submit a pipeline run",
Long: `Submit a pipeline for execution on the abc-cluster platform.

Examples:
  # Run a pipeline by URL
  abc pipeline run --pipeline https://github.com/org/my-pipeline

  # Run with a specific revision and profile
  abc pipeline run --pipeline my-pipeline --revision main --profile test

  # Run with a params file
  abc pipeline run --pipeline my-pipeline --params-file params.yaml`,
RunE: func(cmd *cobra.Command, args []string) error {
return runPipeline(cmd, opts, *serverURL, *accessToken, *workspace, factory)
},
}

cmd.Flags().StringVarP(&opts.pipeline, "pipeline", "p", "", "pipeline name or URL to run (required)")
cmd.Flags().StringVar(&opts.runName, "name", "", "custom name for this run")
cmd.Flags().StringVar(&opts.revision, "revision", "", "pipeline revision (branch, tag, or commit SHA)")
cmd.Flags().StringVar(&opts.profile, "profile", "", "Nextflow config profile(s) to use (comma-separated)")
cmd.Flags().StringVar(&opts.workDir, "work-dir", "", "work directory for pipeline execution")
cmd.Flags().StringVar(&opts.configFile, "config", "", "path to a Nextflow config file to use for this run")
cmd.Flags().StringVar(&opts.paramsFile, "params-file", "", "path to a YAML or JSON file with pipeline parameters")

_ = cmd.MarkFlagRequired("pipeline")

return cmd
}

// runPipeline executes the pipeline run logic.
func runPipeline(cmd *cobra.Command, opts *runOptions, serverURL, accessToken, workspace string, factory ClientFactory) error {
params, err := loadParamsFile(opts.paramsFile)
if err != nil {
return fmt.Errorf("failed to load params file: %w", err)
}

configText, err := loadTextFile(opts.configFile)
if err != nil {
return fmt.Errorf("failed to load config file: %w", err)
}

req := &api.PipelineRunRequest{
Pipeline:   opts.pipeline,
RunName:    opts.runName,
Revision:   opts.revision,
Profile:    opts.profile,
WorkDir:    opts.workDir,
ConfigText: configText,
Params:     params,
}

client := factory(serverURL, accessToken, workspace)
resp, err := client.SubmitPipelineRun(req)
if err != nil {
return fmt.Errorf("pipeline run submission failed: %w", err)
}

fmt.Fprintf(cmd.OutOrStdout(), "Pipeline run submitted successfully.\n")
fmt.Fprintf(cmd.OutOrStdout(), "  Run ID:   %s\n", resp.RunID)
fmt.Fprintf(cmd.OutOrStdout(), "  Run Name: %s\n", resp.RunName)
if resp.WorkflowID != "" {
fmt.Fprintf(cmd.OutOrStdout(), "  Workflow: %s\n", resp.WorkflowID)
}

return nil
}

// loadParamsFile reads and parses a YAML or JSON parameters file.
// Returns nil if paramsFile is empty.
func loadParamsFile(paramsFile string) (map[string]any, error) {
if paramsFile == "" {
return nil, nil
}

data, err := os.ReadFile(paramsFile)
if err != nil {
return nil, fmt.Errorf("could not read params file %q: %w", paramsFile, err)
}

var params map[string]any

// Try JSON first, then YAML.
if json.Valid(data) {
if err := json.Unmarshal(data, &params); err != nil {
return nil, fmt.Errorf("invalid JSON in params file: %w", err)
}
} else {
if err := yaml.Unmarshal(data, &params); err != nil {
return nil, fmt.Errorf("invalid YAML in params file: %w", err)
}
}

return params, nil
}

// loadTextFile reads the content of a file as a string.
// Returns an empty string if path is empty.
func loadTextFile(path string) (string, error) {
if path == "" {
return "", nil
}
data, err := os.ReadFile(path)
if err != nil {
return "", fmt.Errorf("could not read file %q: %w", path, err)
}
return string(data), nil
}
