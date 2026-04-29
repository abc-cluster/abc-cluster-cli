package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/cliutil/advhelp"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

const (
	watchDelay   = 10 * time.Second
	watchTimeout = 5 * time.Minute
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <name-or-url>",
		Short: "Submit a Nextflow pipeline head job to Nomad",
		Long: `Submit a Nextflow pipeline as a head job to the ABC Nomad cluster.

On abc-nodes contexts with an enhanced monitoring floor (Loki / Prometheus /
Grafana Alloy), the head task env includes ABC_NODES_* URLs synced from
capabilities or admin.services; base abc-nodes clusters omit them.

<name-or-url> can be:
  - A saved pipeline name (stored in Nomad Variables via "abc pipeline add")
  - A Nextflow pipeline repository path  e.g. nextflow-io/hello
  - A full GitHub/GitLab URL             e.g. https://github.com/nf-core/rnaseq

CLI flags override any defaults saved for the named pipeline.

EXAMPLES

  # Ad-hoc run of a public pipeline
  abc pipeline run nextflow-io/hello --profile hello

  # Run a saved pipeline with default parameters
  abc pipeline run rnaseq

  # Override params on a saved pipeline
  abc pipeline run rnaseq --params-file custom-params.yaml --revision 3.14.0

  # Dry-run: print generated HCL without submitting
  abc pipeline run nextflow-io/hello --dry-run

  # Submit and stream head job logs
  abc pipeline run rnaseq --wait --logs`,
		Args: cobra.ExactArgs(1),
		RunE: runPipeline,
	}

	// Nextflow run options
	cmd.Flags().String("params-file", "", "YAML/JSON file with Nextflow pipeline parameters")
	cmd.Flags().String("revision", "", "Pipeline revision (branch, tag, or commit SHA)")
	cmd.Flags().String("profile", "", "Nextflow config profile(s), comma-separated")
	cmd.Flags().String("config", "", "Extra nextflow config file to merge")
	cmd.Flags().String("work-dir", "", "Shared work directory: local path or s3:// URI (default: /work/nextflow-work)")
	cmd.Flags().String("host-volume", "", "Nomad host volume name for the work dir (default: nextflow-work; use \"-\" to disable)")
	cmd.Flags().String("node", "", "Pin the head job to this Nomad node hostname (also use --config to constrain child jobs)")

	// Nomad placement
	cmd.Flags().StringSlice("datacenter", nil, "Nomad datacenter(s) (default: dc1)")

	// Head job resource overrides
	cmd.Flags().String("nf-version", "", "Nextflow Docker image tag (default: 25.10.4)")
	cmd.Flags().String("nf-plugin-version", "", "nf-nomad plugin version (default: 0.4.0-edge3)")
	cmd.Flags().Int("cpu", 0, "Head job CPU in MHz (default: 1000)")
	cmd.Flags().Int("memory", 0, "Head job memory in MB (default: 2048)")

	// Job identity
	cmd.Flags().String("name", "", "Override Nomad job name (default: nextflow-head)")

	// Inline parameter overrides (--param key=value, repeatable)
	cmd.Flags().StringArray("param", nil, "Inline pipeline parameter override (key=value, repeatable; merged on top of --params-file)")

	// Resume / session control
	cmd.Flags().Bool("resume", false, "Append -resume to the nextflow run command (checkpoint restart)")
	cmd.Flags().String("session-id", "", "Resume a specific Nextflow session UUID (implies --resume)")

	// Behaviour
	cmd.Flags().Bool("wait", false, "Block until the head job completes")
	cmd.Flags().Bool("logs", false, "Stream head job logs after submit")
	cmd.Flags().Bool("dry-run", false, "Print generated HCL without submitting")
	cmd.Flags().Duration("timeout", watchTimeout, "Max time to wait for head job completion when using --wait (0 = no limit)")

	advhelp.Register(cmd, []string{
		"work-dir",
		"host-volume",
		"datacenter",
		"nf-version",
		"nf-plugin-version",
		"cpu",
		"memory",
		"session-id",
		"timeout",
	})

	return cmd
}

func runPipeline(cmd *cobra.Command, args []string) error {
	nameOrURL := args[0]
	ns := namespaceFromCmd(cmd)

	nc := nomadClientFromCmd(cmd)

	// Try loading a saved pipeline; treat as ad-hoc URL if not found.
	saved, err := loadPipeline(cmd.Context(), nc, nameOrURL, ns)
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "403") || strings.Contains(errLower, "permission denied") {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"  Note: no access to saved pipeline store; treating %q as ad-hoc pipeline reference.\n", nameOrURL)
			saved = nil
		} else {
			return err
		}
	}
	base := saved
	if base == nil {
		base = &PipelineSpec{Repository: nameOrURL}
	}

	// Build CLI override spec from flags.
	override := &PipelineSpec{}
	if v, _ := cmd.Flags().GetString("name"); v != "" {
		override.Name = v
	}
	if v, _ := cmd.Flags().GetString("revision"); v != "" {
		override.Revision = v
	}
	if v, _ := cmd.Flags().GetString("profile"); v != "" {
		override.Profile = v
	}
	if v, _ := cmd.Flags().GetString("work-dir"); v != "" {
		override.WorkDir = v
	}
	if v, _ := cmd.Flags().GetString("host-volume"); v != "" {
		override.HostVolume = v
	}
	if v, _ := cmd.Flags().GetString("node"); v != "" {
		override.NodeConstraint = v
	}
	if v, _ := cmd.Flags().GetStringSlice("datacenter"); len(v) > 0 {
		override.Datacenters = v
	}
	if v, _ := cmd.Flags().GetString("nf-version"); v != "" {
		override.NfVersion = v
	}
	if v, _ := cmd.Flags().GetString("nf-plugin-version"); v != "" {
		override.NfPluginVersion = v
	}
	if v, _ := cmd.Flags().GetInt("cpu"); v != 0 {
		override.CPU = v
	}
	if v, _ := cmd.Flags().GetInt("memory"); v != 0 {
		override.MemoryMB = v
	}
	if configPath, _ := cmd.Flags().GetString("config"); configPath != "" {
		data, err := readFile(configPath)
		if err != nil {
			return fmt.Errorf("reading --config %q: %w", configPath, err)
		}
		override.ExtraConfig = string(data)
	}
	if paramsFile, _ := cmd.Flags().GetString("params-file"); paramsFile != "" {
		params, err := utils.LoadParamsFile(paramsFile)
		if err != nil {
			return fmt.Errorf("reading --params-file: %w", err)
		}
		override.Params = params
	}
	// Inline --param key=value overrides (merged on top of --params-file).
	if paramKVs, _ := cmd.Flags().GetStringArray("param"); len(paramKVs) > 0 {
		if override.Params == nil {
			override.Params = map[string]any{}
		}
		for _, kv := range paramKVs {
			k, v, _ := strings.Cut(kv, "=")
			override.Params[strings.TrimSpace(k)] = v
		}
	}
	if resume, _ := cmd.Flags().GetBool("resume"); resume {
		override.Resume = true
	}
	if sessionID, _ := cmd.Flags().GetString("session-id"); sessionID != "" {
		override.SessionID = sessionID
	}
	if ns != "" {
		override.Namespace = ns
	}

	spec := mergeSpec(base, override)
	spec.defaults()

	// Translate secret://name param values to Nomad template refs for abc-nodes.
	translateSecretParams(spec)

	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Read Nomad connection details for embedding in the head job env block.
	nomadAddr, nomadToken := nomadConnFromCmd(cmd)

	runUUID := newRunUUID()
	hcl := generateHeadJobHCL(spec, nomadAddr, nomadToken, runUUID)

	if dryRun {
		fmt.Fprint(cmd.OutOrStdout(), hcl)
		return nil
	}

	return submitAndWatch(cmd.Context(), cmd, nc, spec, hcl)
}

func nomadConnFromCmd(cmd *cobra.Command) (string, string) {
	addr, _ := cmd.Flags().GetString("nomad-addr")
	if addr == "" {
		addr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	token, _ := cmd.Flags().GetString("nomad-token")
	if token == "" {
		token, _ = cmd.Root().PersistentFlags().GetString("nomad-token")
	}
	if addr == "" || token == "" {
		cfgAddr, cfgToken, _ := utils.NomadDefaultsFromConfig()
		if addr == "" {
			addr = cfgAddr
		}
		if token == "" {
			token = cfgToken
		}
	}
	if addr == "" {
		addr = "http://127.0.0.1:4646"
	}
	return addr, token
}

func submitAndWatch(ctx context.Context, cmd *cobra.Command, nc *utils.NomadClient, spec *PipelineSpec, hcl string) error {
	log := debuglog.FromContext(ctx)

	fmt.Fprintf(cmd.ErrOrStderr(), "  Parsing HCL via Nomad...\n")
	t := time.Now()
	jobJSON, err := nc.ParseHCL(ctx, hcl)
	if err != nil {
		log.LogAttrs(ctx, debuglog.L1, "pipeline.run.failed",
			debuglog.AttrsError("pipeline.hcl_parse", err)...,
		)
		return fmt.Errorf("nomad HCL parse: %w", err)
	}
	log.LogAttrs(ctx, debuglog.L1, "pipeline.hcl_parsed",
		slog.String("op", "pipeline.run"),
		slog.Int("hcl_bytes", len(hcl)),
		slog.Int64("duration_ms", time.Since(t).Milliseconds()),
	)

	if err := nc.PreflightJobTaskDrivers(ctx, jobJSON, cmd.ErrOrStderr()); err != nil {
		log.LogAttrs(ctx, debuglog.L1, "pipeline.run.failed",
			debuglog.AttrsError("pipeline.driver_preflight", err)...,
		)
		return err
	}

	jobName := "nextflow-head"
	if spec.Name != "" {
		jobName = spec.Name
	}
	if slug := utils.ActiveWhoamiSlug(); slug != "" {
		jobName = slug + "-" + jobName
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "  Submitting pipeline head job to Nomad...\n")
	t = time.Now()
	resp, err := nc.RegisterJob(ctx, jobJSON)
	if err != nil {
		log.LogAttrs(ctx, debuglog.L1, "pipeline.run.failed",
			debuglog.AttrsError("pipeline.job_register", err)...,
		)
		return fmt.Errorf("nomad register: %w", err)
	}
	log.LogAttrs(ctx, debuglog.L1, "pipeline.job_submitted",
		debuglog.AttrsJobSubmit("register", jobName, resp.EvalID, spec.Namespace, time.Since(t).Milliseconds())...,
	)

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  Pipeline submitted\n")
	fmt.Fprintf(out, "  Job        %s\n", jobName)
	fmt.Fprintf(out, "  Eval ID    %s\n", resp.EvalID)
	if resp.Warnings != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Warnings: %s\n", resp.Warnings)
	}

	// Fire-and-forget Grafana annotation so the dashboard timeline marks this submit.
	go annotateGrafanaPipelineStart(spec.Name, jobName, resp.EvalID)

	wait, _ := cmd.Flags().GetBool("wait")
	streamLogs, _ := cmd.Flags().GetBool("logs")

	if wait || streamLogs {
		fmt.Fprintln(cmd.ErrOrStderr(), "\n  Waiting for allocation...")
		var w io.Writer = io.Discard
		if streamLogs {
			w = out
		}
		timeout, _ := cmd.Flags().GetDuration("timeout")
		if err := utils.WatchJobLogs(ctx, nc, jobName, spec.Namespace, w, watchDelay, timeout); err != nil {
			return err
		}
		return nil
	}

	fmt.Fprintf(out, "\n  Track progress:\n")
	fmt.Fprintf(out, "    abc job logs %s --follow\n", jobName)
	fmt.Fprintf(out, "    abc job show %s\n", jobName)
	return nil
}

// translateSecretParams rewrites param values of the form "secret://name" to
// the appropriate Nomad template reference for the active context's secrets
// backend. The rewritten string is embedded in params.json inside a Nomad
// template block, so the {{ }} syntax is evaluated at alloc start.
func translateSecretParams(spec *PipelineSpec) {
	if len(spec.Params) == 0 {
		return
	}
	c, err := abccfg.Load()
	if err != nil {
		return
	}
	ctx := c.ActiveCtx()
	caps := ctx.Capabilities

	ns := spec.Namespace
	if ns == "" {
		ns = "default"
	}

	backend := "nomad"
	if caps != nil && (caps.Secrets == "vault" || caps.Secrets == "vault+sealed") {
		backend = "vault"
	}

	for k, v := range spec.Params {
		s, ok := v.(string)
		if !ok {
			continue
		}
		name, found := strings.CutPrefix(s, "secret://")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if backend == "vault" {
			spec.Params[k] = fmt.Sprintf(
				`{{ with secret "secret/data/abc/%s/%s" }}{{ .Data.data.value }}{{ end }}`,
				ns, name,
			)
		} else {
			spec.Params[k] = fmt.Sprintf(
				`{{ with nomadVar "abc/secrets/%s/%s" }}{{ index . "value" }}{{ end }}`,
				ns, name,
			)
		}
	}
}

func newRunUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("run-%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}

// annotateGrafanaPipelineStart writes a point annotation to Grafana so
// pipeline start events appear on the dashboard timeline.
// Called as a goroutine — failure is silently ignored.
func annotateGrafanaPipelineStart(pipelineName, jobName, evalID string) {
	cfg, err := abccfg.Load()
	if err != nil {
		return
	}
	ctx := cfg.ActiveCtx()
	if ctx.Capabilities == nil || !ctx.Capabilities.Monitoring {
		return
	}
	grafanaHTTP, ok := abccfg.GetAdminFloorField(&ctx.Admin.Services, "grafana", "http")
	if !ok || grafanaHTTP == "" {
		return
	}
	user, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, "grafana", "user")
	password, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, "grafana", "password")

	gc := floor.NewGrafanaClient(grafanaHTTP, user, password)
	text := fmt.Sprintf("Pipeline started: %s (job: %s, eval: %s)", pipelineName, jobName, evalID)
	tags := []string{"abc", "pipeline", "started"}
	_ = gc.Annotate(context.Background(), text, tags)
}
