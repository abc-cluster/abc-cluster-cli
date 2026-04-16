package module

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
	"github.com/spf13/cobra"
)

const (
	watchDelay   = 10 * time.Second
	watchTimeout = 5 * time.Minute
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <nf-core/module>",
		Short: "Generate and run an nf-core module driver pipeline on Nomad",
		Long: `Generate a Nextflow module-driver pipeline with nf-pipeline-gen and run it
as a Nomad batch job in one command.

The generated job has two phases:
  1) prestart task: downloads nf-pipeline-gen + nf-core/modules, then runs the JAR
     (module … --outdir … --force; add --pipeline-gen-no-run-manifest to pass --no-run-manifest)
  2) main task: runs the generated driver with Nextflow

For several drivers from one params/config (tool comparison), use nf-pipeline-gen's matrix
subcommand locally or in CI; each abc module run still targets a single module.`,
		Args: cobra.ExactArgs(1),
		RunE: runModule,
	}

	cmd.Flags().String("name", "", "Override Nomad job name (default: module-<module-slug>)")
	cmd.Flags().String("profile", "nomad,test", "Nextflow profile(s) for generated driver run")
	cmd.Flags().String("work-dir", "", "Shared host volume path (default: /work/nextflow-work)")
	cmd.Flags().String("output-prefix", "", "Output prefix for generated module runs (default: s3://user-output/nextflow)")

	cmd.Flags().String("params-file", "", "Optional params.yml to pass to nf-pipeline-gen (default: auto-generated from module meta.yml)")
	cmd.Flags().String("config-file", "", "Optional module.config to pass to nf-pipeline-gen (default: empty config file)")
	cmd.Flags().String("module-revision", "", "Override module revision recorded in generated driver (default: current nf-core/modules commit prefix)")

	cmd.Flags().String("pipeline-gen-repo", "abc-cluster/nf-pipeline-gen", "GitHub repository for nf-pipeline-gen release assets (owner/repo)")
	cmd.Flags().String("pipeline-gen-version", "latest", "nf-pipeline-gen release to use: latest or a specific tag")
	cmd.Flags().String("github-token", utils.EnvOrDefault("GITHUB_TOKEN", "GH_TOKEN"), "GitHub token for release API/download access (or set GITHUB_TOKEN/GH_TOKEN)")

	cmd.Flags().String("nf-version", "", "Nextflow Docker image tag for generate/run tasks (default: 25.10.4)")
	cmd.Flags().String("nf-plugin-version", "", "nf-nomad plugin version for generated pipeline execution config")
	cmd.Flags().Int("cpu", 0, "Main Nextflow task CPU in MHz (default: 1500)")
	cmd.Flags().Int("memory", 0, "Main Nextflow task memory in MB (default: 4096)")
	cmd.Flags().StringSlice("datacenter", nil, "Nomad datacenter(s) (default: dc1)")
	cmd.Flags().String("minio-endpoint", "", "Optional NF_MINIO_ENDPOINT value for generated driver execution")

	cmd.Flags().Bool("wait", false, "Block until module run job completes")
	cmd.Flags().Bool("logs", false, "Stream module run logs after submit")
	cmd.Flags().Bool("dry-run", false, "Print generated HCL without submitting")
	cmd.Flags().Bool("pipeline-gen-no-run-manifest", false, "Pass --no-run-manifest to nf-pipeline-gen (omit run-manifest.json in each driver)")

	return cmd
}

func runModule(cmd *cobra.Command, args []string) error {
	moduleName := args[0]
	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)

	spec := &RunSpec{
		Module: moduleName,
	}
	if v, _ := cmd.Flags().GetString("name"); v != "" {
		spec.JobName = v
	}
	if v, _ := cmd.Flags().GetString("profile"); v != "" {
		spec.Profile = v
	}
	if v, _ := cmd.Flags().GetString("work-dir"); v != "" {
		spec.WorkDir = v
	}
	if v, _ := cmd.Flags().GetString("output-prefix"); v != "" {
		spec.OutputPrefix = v
	}
	if v, _ := cmd.Flags().GetString("module-revision"); v != "" {
		spec.ModuleRevision = v
	}
	if v, _ := cmd.Flags().GetString("pipeline-gen-repo"); v != "" {
		spec.PipelineGenRepo = v
	}
	if v, _ := cmd.Flags().GetString("pipeline-gen-version"); v != "" {
		spec.PipelineGenVersion = v
	}
	if v, _ := cmd.Flags().GetString("github-token"); v != "" {
		spec.GitHubToken = v
	}
	if v, _ := cmd.Flags().GetString("nf-version"); v != "" {
		spec.NfVersion = v
	}
	if v, _ := cmd.Flags().GetString("nf-plugin-version"); v != "" {
		spec.NfPluginVersion = v
	}
	if v, _ := cmd.Flags().GetInt("cpu"); v != 0 {
		spec.CPU = v
	}
	if v, _ := cmd.Flags().GetInt("memory"); v != 0 {
		spec.MemoryMB = v
	}
	if v, _ := cmd.Flags().GetStringSlice("datacenter"); len(v) > 0 {
		spec.Datacenters = v
	}
	if v, _ := cmd.Flags().GetString("minio-endpoint"); v != "" {
		spec.MinioEndpoint = v
	}
	if v, _ := cmd.Flags().GetBool("pipeline-gen-no-run-manifest"); v {
		spec.PipelineGenNoRunManifest = true
	}
	if ns != "" {
		spec.Namespace = ns
	}

	if paramsFile, _ := cmd.Flags().GetString("params-file"); paramsFile != "" {
		data, err := os.ReadFile(paramsFile)
		if err != nil {
			return fmt.Errorf("reading --params-file %q: %w", paramsFile, err)
		}
		spec.ParamsYAMLContent = string(data)
	}
	if configFile, _ := cmd.Flags().GetString("config-file"); configFile != "" {
		data, err := os.ReadFile(configFile)
		if err != nil {
			return fmt.Errorf("reading --config-file %q: %w", configFile, err)
		}
		spec.ConfigYAMLContent = string(data)
	}

	spec.defaults()

	if spec.GitHubToken == "" {
		return fmt.Errorf("missing GitHub token: set --github-token or GITHUB_TOKEN/GH_TOKEN")
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")

	nomadAddr, _ := cmd.Flags().GetString("nomad-addr")
	if nomadAddr == "" {
		nomadAddr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	if nomadAddr == "" {
		nomadAddr = "http://127.0.0.1:4646"
	}
	nomadToken, _ := cmd.Flags().GetString("nomad-token")
	if nomadToken == "" {
		nomadToken, _ = cmd.Root().PersistentFlags().GetString("nomad-token")
	}

	runUUID := newRunUUID()
	hcl := generateModuleRunHCL(spec, nomadAddr, nomadToken, runUUID)

	if dryRun {
		fmt.Fprint(cmd.OutOrStdout(), hcl)
		return nil
	}

	return submitAndWatch(cmd.Context(), cmd, nc, spec, hcl)
}

func submitAndWatch(ctx context.Context, cmd *cobra.Command, nc *utils.NomadClient, spec *RunSpec, hcl string) error {
	log := debuglog.FromContext(ctx)

	fmt.Fprintf(cmd.ErrOrStderr(), "  Parsing HCL via Nomad...\n")
	t := time.Now()
	jobJSON, err := nc.ParseHCL(ctx, hcl)
	if err != nil {
		log.LogAttrs(ctx, debuglog.L1, "module.run.failed",
			debuglog.AttrsError("module.hcl_parse", err)...,
		)
		return fmt.Errorf("nomad HCL parse: %w", err)
	}
	log.LogAttrs(ctx, debuglog.L1, "module.hcl_parsed",
		slog.String("op", "module.run"),
		slog.Int("hcl_bytes", len(hcl)),
		slog.Int64("duration_ms", time.Since(t).Milliseconds()),
	)

	if err := nc.PreflightJobTaskDrivers(ctx, jobJSON, cmd.ErrOrStderr()); err != nil {
		log.LogAttrs(ctx, debuglog.L1, "module.run.failed",
			debuglog.AttrsError("module.driver_preflight", err)...,
		)
		return err
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "  Submitting module run job to Nomad...\n")
	t = time.Now()
	resp, err := nc.RegisterJob(ctx, jobJSON)
	if err != nil {
		log.LogAttrs(ctx, debuglog.L1, "module.run.failed",
			debuglog.AttrsError("module.job_register", err)...,
		)
		return fmt.Errorf("nomad register: %w", err)
	}
	log.LogAttrs(ctx, debuglog.L1, "module.job_submitted",
		debuglog.AttrsJobSubmit("register", spec.JobName, resp.EvalID, spec.Namespace, time.Since(t).Milliseconds())...,
	)

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  Module run submitted\n")
	fmt.Fprintf(out, "  Job        %s\n", spec.JobName)
	fmt.Fprintf(out, "  Eval ID    %s\n", resp.EvalID)
	if resp.Warnings != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Warnings: %s\n", resp.Warnings)
	}

	wait, _ := cmd.Flags().GetBool("wait")
	streamLogs, _ := cmd.Flags().GetBool("logs")

	if wait || streamLogs {
		fmt.Fprintln(cmd.ErrOrStderr(), "\n  Waiting for allocation...")
		var w io.Writer = io.Discard
		if streamLogs {
			w = out
		}
		if err := utils.WatchJobLogs(ctx, nc, spec.JobName, spec.Namespace, w, watchDelay, watchTimeout); err != nil {
			return err
		}
		return nil
	}

	fmt.Fprintf(out, "\n  Track progress:\n")
	fmt.Fprintf(out, "    abc job logs %s --follow\n", spec.JobName)
	fmt.Fprintf(out, "    abc job show %s\n", spec.JobName)
	return nil
}

func newRunUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("run-%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}
