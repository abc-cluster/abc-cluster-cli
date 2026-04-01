package data

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/abc-cluster/abc-cluster-cli/api"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type downloadOptions struct {
	runName    string
	accessions []string
	configFile string
	paramsFile string
	profile    string
	workDir    string
	revision   string
}

func newDownloadCmd(serverURL, accessToken, workspace *string, factory PipelineClientFactory) *cobra.Command {
	opts := &downloadOptions{}

	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download genomics data using nf-core/fetchngs",
		Long: `Submit a fetchngs pipeline run as a data download job in the cluster.

This command submits a head pipeline job for nf-core/fetchngs.
Users can supply a custom Nextflow config and params file.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDownload(cmd, opts, *serverURL, *accessToken, *workspace, factory)
		},
	}

	cmd.Flags().StringVar(&opts.runName, "name", "", "custom name for this download run")
	cmd.Flags().StringSliceVar(&opts.accessions, "accession", nil, "accession(s) to fetch (repeatable)")
	cmd.Flags().StringVar(&opts.configFile, "config", "", "path to a Nextflow config file")
	cmd.Flags().StringVar(&opts.paramsFile, "params-file", "", "path to a YAML/JSON params file")
	cmd.Flags().StringVar(&opts.profile, "profile", "", "Nextflow profile(s) to use")
	cmd.Flags().StringVar(&opts.workDir, "work-dir", "", "work directory for pipeline execution")
	cmd.Flags().StringVar(&opts.revision, "revision", "", "pipeline revision (branch/tag/commit)")

	return cmd
}

func runDownload(cmd *cobra.Command, opts *downloadOptions, serverURL, accessToken, workspace string, factory PipelineClientFactory) error {
	if len(opts.accessions) == 0 && opts.paramsFile == "" {
		return fmt.Errorf("must provide at least one --accession or --params-file")
	}

	params, err := loadParamsFile(opts.paramsFile)
	if err != nil {
		return fmt.Errorf("failed to load params file: %w", err)
	}

	if len(opts.accessions) > 0 {
		if params == nil {
			params = map[string]any{}
		}
		if len(opts.accessions) == 1 {
			params["accession"] = opts.accessions[0]
		} else {
			params["accession"] = opts.accessions
		}
	}

	configText, err := loadTextFile(opts.configFile)
	if err != nil {
		return fmt.Errorf("failed to load config file: %w", err)
	}

	req := &api.PipelineRunRequest{
		Pipeline:   "https://github.com/nf-core/fetchngs",
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
		return fmt.Errorf("data download pipeline submission failed: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Data download pipeline submitted successfully.\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  Run ID:   %s\n", resp.RunID)
	if resp.RunName != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Run Name: %s\n", resp.RunName)
	}
	if resp.WorkflowID != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Workflow: %s\n", resp.WorkflowID)
	}

	return nil
}

func loadParamsFile(paramsFile string) (map[string]any, error) {
	if paramsFile == "" {
		return nil, nil
	}

	data, err := os.ReadFile(paramsFile)
	if err != nil {
		return nil, fmt.Errorf("could not read params file %q: %w", paramsFile, err)
	}

	var params map[string]any
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
