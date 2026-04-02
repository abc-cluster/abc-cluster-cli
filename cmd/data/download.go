package data

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/api"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type downloadOptions struct {
	runName     string
	accessions  []string
	configFile  string
	paramsFile  string
	profile     string
	workDir     string
	revision    string
	tool        string
	driver      string
	source      string
	destination string
	urlFile     string
	parallel    int
	toolArgs    string
}

const defaultDockerImage = "ghcr.io/abc-cluster/abc-data-transfer:v2026-01-01"

var dockerImageByTool = map[string]string{
	"aria2":    "quay.io/biocontainers/aria2:1.36.0",
	"rclone":   "quay.io/rclone/rclone:1.77.0",
	"wget":     "busybox:1.36.0",
	"s5cmd":    "quay.io/s5cmd/s5cmd:2.1.0",
	"nextflow": "nextflow/nextflow:25.10.4",
}

func newDownloadCmd(serverURL, accessToken, workspace *string, factory PipelineClientFactory) *cobra.Command {
	opts := &downloadOptions{}

	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download data via various tools",
		Long: `Download data via selected tool and dispatch as Nomad job.

Supports driver selection (exec/docker) with pinned docker image.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDownload(cmd, opts, *serverURL, *accessToken, *workspace, factory)
		},
	}

	cmd.Flags().StringVar(&opts.runName, "name", "", "custom job name")
	cmd.Flags().StringSliceVar(&opts.accessions, "accession", nil, "accession(s) for nextflow")
	cmd.Flags().StringVar(&opts.configFile, "config", "", "nextflow config file")
	cmd.Flags().StringVar(&opts.paramsFile, "params-file", "", "nextflow params file")
	cmd.Flags().StringVar(&opts.profile, "profile", "", "nextflow profile")
	cmd.Flags().StringVar(&opts.workDir, "work-dir", "", "nextflow work dir")
	cmd.Flags().StringVar(&opts.revision, "revision", "", "nextflow revision tag/commit")

	cmd.Flags().StringVar(&opts.tool, "tool", "aria2", "download tool: aria2,rclone,wget,s5cmd,nextflow")
	cmd.Flags().StringVar(&opts.driver, "driver", "exec", "nomad driver: exec or docker")
	cmd.Flags().StringVar(&opts.source, "source", "", "source URL/path")
	cmd.Flags().StringVar(&opts.destination, "dest", "", "destination path")
	cmd.Flags().StringVar(&opts.urlFile, "url-file", "", "newline-separated URL file")
	cmd.Flags().IntVar(&opts.parallel, "parallel", 4, "parallelism")
	cmd.Flags().StringVar(&opts.toolArgs, "tool-args", "", "extra flags for tool")

	return cmd
}

func runDownload(cmd *cobra.Command, opts *downloadOptions, serverURL, accessToken, workspace string, factory PipelineClientFactory) error {
	if opts.tool == "" {
		opts.tool = "aria2"
	}
	if opts.driver == "" {
		opts.driver = "exec"
	}

	tool := strings.ToLower(opts.tool)
	driver := strings.ToLower(opts.driver)

	if tool != "nextflow" {
		if driver != "exec" && driver != "docker" {
			return fmt.Errorf("unsupported driver %q", driver)
		}
		if driver == "docker" && opts.destination == "" {
			opts.destination = "/tmp/abc-data-download"
		}
		downloadsScript, err := buildToolScript(opts, serverURL, accessToken, workspace)
		if err != nil {
			return err
		}
		return submitJobWithDriver(cmd, opts, downloadsScript, driver)
	}

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

func shellEscape(str string) string {
	return "'" + strings.ReplaceAll(str, "'", "'\"'\"'") + "'"
}

func buildToolCommand(opts *downloadOptions) (string, error) {
	dest := opts.destination
	if dest == "" {
		dest = "/tmp/abc-data-download"
	}
	if opts.source == "" && opts.urlFile == "" {
		return "", fmt.Errorf("either --source or --url-file is required for tool %q", opts.tool)
	}

	parallel := opts.parallel
	if parallel <= 0 {
		parallel = 4
	}

	var cmd string
	extra := strings.TrimSpace(opts.toolArgs)
	if extra != "" {
		extra = " " + extra
	}

	switch strings.ToLower(opts.tool) {
	case "aria2":
		if opts.urlFile != "" {
			cmd = fmt.Sprintf("aria2c --input-file=%s --dir=%s --max-concurrent-downloads=%d --max-overall-download-limit=0%s", shellEscape(opts.urlFile), shellEscape(dest), parallel, extra)
		} else {
			cmd = fmt.Sprintf("aria2c -x %d -s %d -d %s %s%s", parallel, parallel, shellEscape(dest), shellEscape(opts.source), extra)
		}
	case "rclone":
		if opts.urlFile != "" {
			cmd = fmt.Sprintf("rclone copy --transfers=%d --files-from=%s %s %s%s", parallel, shellEscape(opts.urlFile), shellEscape(opts.source), shellEscape(dest), extra)
		} else {
			cmd = fmt.Sprintf("rclone copy --transfers=%d %s %s%s", parallel, shellEscape(opts.source), shellEscape(dest), extra)
		}
	case "wget":
		if opts.urlFile != "" {
			cmd = fmt.Sprintf("cat %s | xargs -n1 -P %d wget -c -P %s%s", shellEscape(opts.urlFile), parallel, shellEscape(dest), extra)
		} else {
			cmd = fmt.Sprintf("wget -c -P %s %s%s", shellEscape(dest), shellEscape(opts.source), extra)
		}
	case "s5cmd":
		if opts.urlFile != "" {
			cmd = fmt.Sprintf("s5cmd --jobs %d cp --from-file %s %s%s", parallel, shellEscape(opts.urlFile), shellEscape(dest), extra)
		} else {
			cmd = fmt.Sprintf("s5cmd --jobs %d cp %s %s%s", parallel, shellEscape(opts.source), shellEscape(dest), extra)
		}
	default:
		return "", fmt.Errorf("unsupported tool %q", opts.tool)
	}

	return cmd, nil
}

func buildToolScript(opts *downloadOptions, serverURL, accessToken, workspace string) (string, error) {
	cmdLine, err := buildToolCommand(opts)
	if err != nil {
		return "", err
	}

	dest := opts.destination
	if dest == "" {
		dest = "/tmp/abc-data-download"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("mkdir -p %s\n", shellEscape(dest)))

	// Task 1: download using selected tool
	sb.WriteString("echo '=== TASK 1: Downloading files with chosen tool ==='\n")
	sb.WriteString(cmdLine + "\n")

	// Task 2: upload to TUS endpoint (optional, only if destination is non-empty)
	if opts.destination != "" {
		uploadCmd := fmt.Sprintf("abc data upload --url=%s --access-token=%s --workspace=%s", shellEscape(serverURL), shellEscape(accessToken), shellEscape(workspace))
		sb.WriteString("echo '=== TASK 2: Uploading downloaded artifacts to tusd endpoint ==='\n")
		sb.WriteString(fmt.Sprintf("find %s -type f -print0 | while IFS= read -r -d '' f; do %s \"$f\"; done\n", shellEscape(dest), uploadCmd))
	} else {
		sb.WriteString("echo '=== TASK 2: No destination provided, skipping upload ==='\n")
	}

	return sb.String(), nil
}

func submitJobWithDriver(cmd *cobra.Command, opts *downloadOptions, taskBody, driver string) error {
	// create wrapper script with prefix
	tmpScript, err := os.CreateTemp("", "abc-data-download-*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temp script: %w", err)
	}
	defer os.Remove(tmpScript.Name())

	if _, err := tmpScript.WriteString("#!/bin/sh\nset -euo pipefail\n"); err != nil {
		return fmt.Errorf("failed to write script header: %w", err)
	}
	if opts.runName != "" {
		if _, err := tmpScript.WriteString(fmt.Sprintf("#ABC --name=%s\n", opts.runName)); err != nil {
			return err
		}
	}
	if _, err := tmpScript.WriteString(fmt.Sprintf("#ABC --driver=%s\n", driver)); err != nil {
		return err
	}
	if _, err := tmpScript.WriteString(taskBody); err != nil {
		return err
	}
	if err := tmpScript.Close(); err != nil {
		return fmt.Errorf("unable to close temp script: %w", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("unable to resolve executable path: %w", err)
	}

	image := defaultDockerImage
	if img, ok := dockerImageByTool[strings.ToLower(opts.tool)]; ok {
		image = img
	}

	var jobArgs []string
	if driver == "docker" {
		jobArgs = []string{"job", "run", "--submit", "--driver", "docker", "--driver.config", fmt.Sprintf("image=%s", image), tmpScript.Name()}
	} else {
		jobArgs = []string{"job", "run", "--submit", "--driver=exec", tmpScript.Name()}
	}
	if opts.runName != "" {
		if driver == "docker" {
			jobArgs = []string{"job", "run", "--submit", "--name", opts.runName, "--driver", "docker", "--driver.config", fmt.Sprintf("image=%s", image), tmpScript.Name()}
		} else {
			jobArgs = []string{"job", "run", "--submit", "--name", opts.runName, "--driver=exec", tmpScript.Name()}
		}
	}

	task := exec.Command(execPath, jobArgs...)
	task.Stdout = cmd.OutOrStdout()
	task.Stderr = cmd.ErrOrStderr()
	return task.Run()
}
