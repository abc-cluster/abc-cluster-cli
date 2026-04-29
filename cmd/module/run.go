package module

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/module/samplesheet"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/cliutil/advhelp"
	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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

EXAMPLES

  # Run the module's bundled tests (fixtures staged from nf-core/test-datasets)
  abc module run nf-core/seqkit/stats --test

  # Run with your own params and config
  abc module run nf-core/fastqc --params-file params.yml --config-file fastqc.config

For several drivers from one params/config (tool comparison), use nf-pipeline-gen's matrix
subcommand locally or in CI; each abc module run still targets a single module.`,
		Args: cobra.ExactArgs(1),
		RunE: runModule,
	}

	cmd.Flags().String("name", "", "Override Nomad job name (default: module-<module-slug>)")
	cmd.Flags().String("profile", "test", "Nextflow profile(s) for generated driver run (auto-includes 'test' when --test is set)")
	cmd.Flags().String("work-dir", "", "Mount path inside the alloc for the shared work dir (default: /opt/nomad/scratch/nextflow-work)")
	cmd.Flags().String("host-volume", "", "Name of the Nomad host volume to mount as the shared work dir (default: scratch — registered on every abc-managed node)")
	cmd.Flags().String("driver", "", "Nomad task driver for the prestart + run tasks (default: docker; use 'containerd-driver' for containerd-only nodes like aither)")
	cmd.Flags().String("nf-plugin-zip-url", "", "Optional URL to a Nextflow plugin .zip that the run task fetches before launching nextflow (e.g. a patched nf-nomad)")
	cmd.Flags().String("output-prefix", "", "Output prefix for generated module runs (default: s3://user-output/nextflow)")

	cmd.Flags().String("params-file", "", "Optional params.yml to pass to nf-pipeline-gen (default: auto-generated from module meta.yml)")
	cmd.Flags().String("config-file", "", "Optional module.config to pass to nf-pipeline-gen (default: empty config file)")
	cmd.Flags().String("samplesheet", "", "Local CSV samplesheet for the run; staged into the prestart task and validated against the module's meta.yml via nf-pipeline-gen --validate-samplesheet before driver generation. Use 'abc module samplesheet emit' to scaffold one.")
	cmd.Flags().String("module-revision", "", "Override module revision recorded in generated driver (default: current nf-core/modules commit prefix)")

	cmd.Flags().String("pipeline-gen-repo", "abc-cluster/nf-pipeline-gen", "GitHub repository for nf-pipeline-gen release assets (owner/repo)")
	cmd.Flags().String("pipeline-gen-version", "latest", "nf-pipeline-gen release to use: latest or a specific tag")
	cmd.Flags().String("pipeline-gen-url-base", "", "Direct URL base for the JAR (e.g. http://rustfs.aither/releases/nf-pipeline-gen). When set, prestart fetches <base>/<version>/pipeline-gen.jar and skips GitHub")
	cmd.Flags().String("github-token", utils.EnvOrDefault("GITHUB_TOKEN", "GH_TOKEN"), "GitHub token for release API/download access (or set GITHUB_TOKEN/GH_TOKEN)")

	cmd.Flags().String("nf-version", "", "Nextflow Docker image tag for generate/run tasks (default: 25.10.4)")
	cmd.Flags().String("nf-plugin-version", "", "nf-nomad plugin version for generated pipeline execution config")
	cmd.Flags().Int("cpu", 0, "Main Nextflow task CPU in MHz (default: 1500)")
	cmd.Flags().Int("memory", 0, "Main Nextflow task memory in MB (default: 4096)")
	cmd.Flags().StringSlice("datacenter", nil, "Nomad datacenter(s) (default: dc1)")
	cmd.Flags().String("s3-endpoint", "", "S3 endpoint URL for the generated driver (sets NF_S3_ENDPOINT in the run task; e.g. http://rustfs.aither:9900)")
	cmd.Flags().String("s3-access-key", "", "S3 access key for the generated driver (sets AWS_ACCESS_KEY_ID; falls back to AWS_ACCESS_KEY_ID env var)")
	cmd.Flags().String("s3-secret-key", "", "S3 secret key for the generated driver (sets AWS_SECRET_ACCESS_KEY; falls back to AWS_SECRET_ACCESS_KEY env var)")

	cmd.Flags().Bool("test", false, "Run the module's bundled tests/main.nf.test fixtures (staged from nf-core/test-datasets); forces the 'test' profile and ignores --params-file inputs")
	cmd.Flags().Bool("wait", false, "Block until module run job completes")
	cmd.Flags().Bool("logs", false, "Stream module run logs after submit")
	cmd.Flags().Bool("dry-run", false, "Print generated HCL without submitting")
	cmd.Flags().Bool("pipeline-gen-no-run-manifest", false, "Pass --no-run-manifest to nf-pipeline-gen (omit run-manifest.json in each driver)")

	advhelp.Register(cmd, []string{
		"work-dir",
		"output-prefix",
		"module-revision",
		"pipeline-gen-repo",
		"pipeline-gen-version",
		"github-token",
		"nf-version",
		"nf-plugin-version",
		"cpu",
		"memory",
		"datacenter",
		"s3-endpoint",
		"pipeline-gen-no-run-manifest",
	})

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
	if v, _ := cmd.Flags().GetString("host-volume"); v != "" {
		spec.HostVolume = v
	}
	if v, _ := cmd.Flags().GetString("driver"); v != "" {
		spec.TaskDriver = v
	}
	if v, _ := cmd.Flags().GetString("nf-plugin-zip-url"); v != "" {
		spec.NfPluginZipURL = v
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
	if v, _ := cmd.Flags().GetString("pipeline-gen-url-base"); v != "" {
		spec.PipelineGenURLBase = v
		// Auto-resolve hostname locally so the prestart container can reach
		// the URL even when its DNS doesn't include Tailscale magicDNS
		// (rustfs.aither, etc.). Skipped silently if URL is malformed or
		// hostname is already an IP.
		if r := autoResolveCurlOverride(v); r != "" {
			spec.PipelineGenURLResolve = r
		}
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
	if v, _ := cmd.Flags().GetString("s3-endpoint"); v != "" {
		spec.S3Endpoint = v
	}
	if v, _ := cmd.Flags().GetString("s3-access-key"); v != "" {
		spec.S3AccessKey = v
	} else if v := os.Getenv("AWS_ACCESS_KEY_ID"); v != "" {
		spec.S3AccessKey = v
	}
	if v, _ := cmd.Flags().GetString("s3-secret-key"); v != "" {
		spec.S3SecretKey = v
	} else if v := os.Getenv("AWS_SECRET_ACCESS_KEY"); v != "" {
		spec.S3SecretKey = v
	}
	if v, _ := cmd.Flags().GetBool("pipeline-gen-no-run-manifest"); v {
		spec.PipelineGenNoRunManifest = true
	}
	if v, _ := cmd.Flags().GetBool("test"); v {
		spec.TestMode = true
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
	if sheetPath, _ := cmd.Flags().GetString("samplesheet"); sheetPath != "" {
		// Local pre-flight: catches obvious shape problems before submitting
		// the alloc. The authoritative validation runs cluster-side via
		// `pipeline-gen --validate-samplesheet`.
		pre, err := samplesheet.Preflight(sheetPath)
		if err != nil {
			return fmt.Errorf("samplesheet pre-flight: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), pre.HumanReport)
		spec.SamplesheetCSVContent = string(pre.CSVBytes)
		spec.SamplesheetSourcePath = pre.Path
	}

	spec.defaults()

	if slug := utils.ActiveWhoamiSlug(); slug != "" {
		spec.JobName = slug + "-" + spec.JobName
	}

	if spec.GitHubToken == "" && spec.PipelineGenURLBase == "" {
		return fmt.Errorf("missing GitHub token: set GITHUB_TOKEN or GH_TOKEN env var, or pass --pipeline-gen-url-base to fetch the JAR from a mirror (see --help --advanced)")
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

	if err := runPreflight(cmd.Context(), cmd.ErrOrStderr(), spec, nomadAddr, nomadToken); err != nil {
		return err
	}

	switch {
	case spec.S3AccessKey != "" && spec.S3SecretKey != "":
		fmt.Fprintf(cmd.ErrOrStderr(), "  Preflight  S3 creds     embedded in run task env (key=%s…)\n", maskKey(spec.S3AccessKey))
	case strings.HasPrefix(spec.OutputPrefix, "s3://"):
		fmt.Fprintf(cmd.ErrOrStderr(), "  Preflight  S3 creds     none provided — driver will run without keys (works for fully-anonymous buckets)\n")
	}

	if !cmd.Flags().Changed("wait") && !cmd.Flags().Changed("logs") && stdoutIsTTY() {
		_ = cmd.Flags().Set("wait", "true")
		_ = cmd.Flags().Set("logs", "true")
		fmt.Fprintln(cmd.ErrOrStderr(), "  Interactive terminal detected — streaming logs (override with --wait=false --logs=false)")
	}

	return submitAndWatch(cmd.Context(), cmd, nc, spec, hcl)
}

func stdoutIsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// autoResolveCurlOverride returns a curl --resolve override string
// (host:port:ip) for the given URL by performing a local DNS lookup. Returns
// "" when the URL's host is already an IP literal, the URL is malformed, or
// the lookup fails. The CLI runs on a host with Tailscale magicDNS, so names
// like rustfs.aither resolve here but not inside containerd-driver containers.
func autoResolveCurlOverride(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return "" // already an IP, no override needed
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		default:
			port = "80"
		}
	}
	ips, err := net.LookupHost(host)
	if err != nil || len(ips) == 0 {
		return ""
	}
	return host + ":" + port + ":" + ips[0]
}

func maskKey(k string) string {
	if len(k) <= 4 {
		return strings.Repeat("*", len(k))
	}
	return k[:4] + strings.Repeat("*", len(k)-4)
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
