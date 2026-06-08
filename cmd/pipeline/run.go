package pipeline

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/cliutil/advhelp"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/abc-cluster/abc-cluster-cli/internal/runner"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/wave"
	"github.com/spf13/cobra"
)

const (
	watchDelay   = 10 * time.Second
	watchTimeout = 5 * time.Minute
)

// parseHeadEnv turns `--env` entries into a name→value map. Each entry is
// either `KEY=VALUE`, or a bare `KEY` whose value is read from the caller's
// environment (so a secret like GITHUB_TOKEN need not appear in argv/history).
func parseHeadEnv(entries []string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range entries {
		if strings.TrimSpace(e) == "" {
			continue
		}
		// Trim only the key; preserve the value verbatim (env values may
		// legitimately contain leading/trailing spaces).
		if k, v, ok := strings.Cut(e, "="); ok {
			k = strings.TrimSpace(k)
			if k == "" {
				return nil, fmt.Errorf("--env %q: empty key", e)
			}
			out[k] = v
			continue
		}
		// bare KEY → read from the caller's environment.
		key := strings.TrimSpace(e)
		v, ok := os.LookupEnv(key)
		if !ok {
			return nil, fmt.Errorf("--env %s: no value provided and %s is not set in your environment (use --env %s=VALUE)", key, key, key)
		}
		out[key] = v
	}
	return out, nil
}

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
	cmd.Flags().String("work-dir", "", "Shared work directory: local path or s3:// URI. When omitted, auto-derived under s3://<group-bucket>/<scope>/<user>/workdir/<run-tag>/ — see --share and --visibility.")

	// Visibility scope for auto-derived work-dir + outdir paths. Default is
	// "user" (private to the submitter + group admins). --share is a boolean
	// shortcut for --visibility=group (writable by submitter, readable by
	// group members). Spec: abc-bucket-layout-phase-1.md.
	cmd.Flags().Bool("share", false,
		"Make auto-derived work-dir + results paths group-readable (shortcut for --visibility=group)")
	cmd.Flags().String("visibility", "",
		"Visibility scope for auto-derived work-dir + results: 'user' (default; private) or 'group' (shared with group members). "+
			"Overrides --share when both set. No effect on explicit --work-dir / --param outdir=…")

	// Node-pool placement. Heads + workers go to different pools by
	// default on multi-pool clusters (platform / compute on seedling).
	// Operator-pinned via active context's admin.services.nomad.{head_pool,
	// worker_pool}; per-run override via these flags.
	cmd.Flags().String("head-pool", "",
		"Nomad node-pool the pipeline head must land in. Overrides the active context's admin.services.nomad.head_pool. "+
			"Empty = use the active context's default (typically 'platform').")
	cmd.Flags().String("worker-pool", "",
		"Nomad node-pool nf-nomad workers should land in. Overrides the active context's admin.services.nomad.worker_pool. "+
			"Bypassed when --pin-workers is set. Empty = use the active context's default (typically 'compute').")
	cmd.Flags().String("host-volume", "", "Nomad host volume name for the work dir (default: nextflow-work; use \"-\" to disable)")
	cmd.Flags().String("node", "", "Pin the head job to this Nomad node hostname (workers spread freely; combine with --pin-workers for single-host runs)")
	cmd.Flags().Bool("pin-workers", false, "When --node is set, ALSO pin every spawned process to that node (single-host run; needed when there is no shared FS / nf-rclone)")
	cmd.Flags().String("worker-exclude-host", "", "Force every spawned process OFF this hostname (combine with --node to enforce a true head≠worker distributed test)")

	// Nomad placement
	cmd.Flags().StringSlice("datacenter", nil, "Nomad datacenter(s) (default: dc1)")

	// Head job resource overrides
	cmd.Flags().String("nf-version", "", "Nextflow Docker image tag (default: 25.10.4)")
	// nf-nomad defaults to the newest published release. To pin it, use the
	// general --plugin flag (e.g. --plugin nf-nomad@0.4.0-edge8); there is no
	// dedicated --nf-plugin-version on `run` — --plugin / --dev-plugins cover it.
	cmd.Flags().Int("cpu", 0, "Head job CPU in MHz (default: 1000)")
	cmd.Flags().Int("memory", 0, "Head job memory in MB (default: 2048)")
	cmd.Flags().Int("disk", 0, "Head job ephemeral disk in MB for foreign-input staging (default: 4096)")
	cmd.Flags().Float64("scratch-gb", 0, "Scratch storage reservation (GB) for accounting/emissions reporting")

	// Job identity
	cmd.Flags().String("name", "", "Override Nomad job name (default: nextflow-head)")

	// Auto-attach to active project / investigation (per spec abc-investigation §E).
	cmd.Flags().String("project", "", "Override active project (slug or ID); --no-project disables")
	cmd.Flags().String("investigation", "", "Override active investigation (slug or ID); --no-investigation disables")
	cmd.Flags().Bool("no-project", false, "Skip project auto-attach")
	cmd.Flags().Bool("no-investigation", false, "Skip investigation auto-attach")

	// Inline parameter overrides (--param key=value, repeatable)
	cmd.Flags().StringArray("param", nil, "Inline pipeline parameter override (key=value, repeatable; merged on top of --params-file)")
	cmd.Flags().StringArray("env", nil, "Inject an env var into the head job (KEY=VALUE, repeatable; a bare KEY reads the value from your current environment). Use for a private-repo GITHUB_TOKEN, etc.")
	cmd.Flags().String("git-token", "", "Convenience for a private-repo head-job clone: sets GITHUB_TOKEN in the head job to this value. (To read it from your environment instead, use --env GITHUB_TOKEN.)")

	// Resume / session control
	cmd.Flags().Bool("resume", false, "Append -resume to the nextflow run command (checkpoint restart)")
	cmd.Flags().String("session-id", "", "Resume a specific Nextflow session UUID (implies --resume)")

	// Wave container augmentation.
	cmd.Flags().Bool("wave", false,
		"Enable Wave container augmentation for this pipeline run. "+
			"Routes to abc-wave (if configured and healthy) or falls back to wave.seqera.io.")
	cmd.Flags().String("wave-endpoint", "",
		"Override Wave endpoint URL directly (bypasses abc-wave probe). "+
			"Use \"seqera\" as shorthand for https://wave.seqera.io.")
	cmd.Flags().Bool("fusion", false,
		"Enable Fusion filesystem alongside Wave (requires --wave and an s3:// work dir). "+
			"Always routes to wave.seqera.io — Fusion is not supported by Wave Lite.")

	// Container runtime selection.
	cmd.Flags().Bool("singularity", false,
		"Use Singularity as the container runtime instead of Docker. "+
			"With --wave, enables ociAutoPull so Singularity converts the Wave-augmented OCI "+
			"image to SIF locally — no Wave-side SIF build required (compatible with Wave Lite).")
	cmd.Flags().Bool("apptainer", false,
		"Use Apptainer as the container runtime instead of Docker. "+
			"With --wave, enables ociAutoPull so Apptainer converts the Wave-augmented OCI "+
			"image to SIF locally — no Wave-side SIF build required (compatible with Wave Lite).")

	// Dev plugin set — opt the run into the cluster's nf-abc-cluster-dev meta-plugin bundle.
	cmd.Flags().Bool("dev-plugins", false,
		"Load the nf-abc-cluster-dev plugin bundle into the head container "+
			"(requires `abc admin tools push` to have shipped the bundle)")

	// Pin specific published plugin versions — repeatable, format `id@version`.
	// Use to test a published plugin release without --dev-plugins, e.g.
	//   --plugin nf-nomad@0.4.0-edge7 --plugin nf-nomad-s5cmd@0.1.0
	// Plugins listed here go directly into the generated plugins {} block;
	// Nextflow resolves them from its standard registry. Mutually exclusive
	// with --dev-plugins (validated below).
	cmd.Flags().StringArray("plugin", nil,
		"Pin a Nextflow plugin to a specific version (repeatable, format `id@version`). "+
			"Use for testing published plugin releases without the dev bundle.")

	// Dev Nextflow binary — replace the image's built-in nextflow with a custom fork.
	// Independent of --dev-plugins; the two flags can be combined freely.
	cmd.Flags().Bool("dev-nextflow", false,
		"Replace the head container's nextflow binary with the custom fork registered "+
			"as nextflow-dev in tools.toml (requires `abc admin tools push` to have shipped it)")

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

	if err := validatePipelineRef(nameOrURL); err != nil {
		return err
	}

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
	if pin, _ := cmd.Flags().GetBool("pin-workers"); pin {
		override.PinWorkers = true
	}
	if v, _ := cmd.Flags().GetString("worker-exclude-host"); v != "" {
		override.WorkerExcludeHost = v
	}
	if v, _ := cmd.Flags().GetString("head-pool"); v != "" {
		override.HeadPool = v
	}
	if v, _ := cmd.Flags().GetString("worker-pool"); v != "" {
		override.WorkerPool = v
	}
	if v, _ := cmd.Flags().GetStringSlice("datacenter"); len(v) > 0 {
		override.Datacenters = v
	}
	if v, _ := cmd.Flags().GetString("nf-version"); v != "" {
		override.NfVersion = v
	}
	if v, _ := cmd.Flags().GetInt("cpu"); v != 0 {
		override.CPU = v
	}
	if v, _ := cmd.Flags().GetInt("memory"); v != 0 {
		override.MemoryMB = v
	}
	if v, _ := cmd.Flags().GetInt("disk"); v != 0 {
		override.HeadDiskMB = v
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
	// --env KEY=VALUE | KEY (repeatable) + --git-token convenience → head-job env.
	if envKVs, _ := cmd.Flags().GetStringArray("env"); len(envKVs) > 0 {
		ev, err := parseHeadEnv(envKVs)
		if err != nil {
			return err
		}
		if override.ExtraEnv == nil {
			override.ExtraEnv = map[string]string{}
		}
		for k, v := range ev {
			override.ExtraEnv[k] = v
		}
	}
	if gt, _ := cmd.Flags().GetString("git-token"); gt != "" {
		if override.ExtraEnv == nil {
			override.ExtraEnv = map[string]string{}
		}
		override.ExtraEnv["GITHUB_TOKEN"] = gt
	}
	if resume, _ := cmd.Flags().GetBool("resume"); resume {
		override.Resume = true
	}
	if sessionID, _ := cmd.Flags().GetString("session-id"); sessionID != "" {
		// --session-id implies --resume (was previously inferred inside the
		// HCL generator; now that fresh runs also carry a SessionID we need
		// to set Resume explicitly here).
		override.SessionID = sessionID
		override.Resume = true
	}
	if devPlugins, _ := cmd.Flags().GetBool("dev-plugins"); devPlugins {
		override.DevPlugins = true
	}
	// --plugin id@version (repeatable). Parsed into PluginRef entries; the
	// generator's plugins {} block emits them verbatim. Empty value (no flag)
	// leaves override.Plugins nil so the spec's saved plugins (if any) win.
	if pluginSpecs, _ := cmd.Flags().GetStringArray("plugin"); len(pluginSpecs) > 0 {
		for _, ps := range pluginSpecs {
			ps = strings.TrimSpace(ps)
			if ps == "" {
				continue
			}
			id, ver, _ := strings.Cut(ps, "@")
			id = strings.TrimSpace(id)
			ver = strings.TrimSpace(ver)
			if id == "" {
				return fmt.Errorf("--plugin: empty plugin id in %q (expected `id@version`)", ps)
			}
			override.Plugins = append(override.Plugins, PluginRef{ID: id, Version: ver})
		}
	}
	if devNextflow, _ := cmd.Flags().GetBool("dev-nextflow"); devNextflow {
		override.DevNextflow = true
	}
	if ns != "" {
		override.Namespace = ns
	} else if c, err := abccfg.Load(); err == nil {
		// Fall back to the active context's Nomad namespace so worker jobs land
		// in the right namespace without requiring --namespace on every invocation.
		if ctxNS := c.ActiveCtx().NomadNamespace(); ctxNS != "" && ctxNS != "default" {
			override.Namespace = ctxNS
		}
	}

	// Container runtime selection — mutually exclusive.
	singularityFlag, _ := cmd.Flags().GetBool("singularity")
	apptainerFlag, _ := cmd.Flags().GetBool("apptainer")
	fusionFlag, _ := cmd.Flags().GetBool("fusion")
	if singularityFlag && apptainerFlag {
		return fmt.Errorf("--singularity and --apptainer are mutually exclusive")
	}
	if (singularityFlag || apptainerFlag) && fusionFlag {
		return fmt.Errorf("Fusion is not compatible with Singularity/Apptainer (Fusion requires Docker/containerd)")
	}
	switch {
	case singularityFlag:
		override.ContainerRuntime = "singularity"
	case apptainerFlag:
		override.ContainerRuntime = "apptainer"
	}

	// Wave endpoint resolution.
	// --wave-endpoint "seqera" is a convenience shorthand for the public service.
	// --wave alone triggers hybrid routing: probe abc-wave; fall back to Seqera.
	// --fusion forces Seqera Wave regardless of abc-wave config (Wave Lite has no Fusion).
	// --fusion / --wave are silently ignored when neither --wave nor --wave-endpoint is set.
	waveEndpointFlag, _ := cmd.Flags().GetString("wave-endpoint")
	waveFlag, _ := cmd.Flags().GetBool("wave")
	if waveEndpointFlag == "seqera" {
		waveEndpointFlag = wave.SeqeraWaveURL
	}
	if waveEndpointFlag != "" {
		override.WaveEndpoint = waveEndpointFlag
		override.FusionEnabled = fusionFlag
	} else if waveFlag || fusionFlag {
		if fusionFlag {
			// Fusion always uses Seqera Wave — Wave Lite has no Fusion support.
			override.WaveEndpoint = wave.SeqeraWaveURL
			override.FusionEnabled = true
			fmt.Fprintf(cmd.ErrOrStderr(), "  Wave/Fusion: routing to %s (Fusion requires Seqera Wave)\n", wave.SeqeraWaveURL)
		} else {
			abcWaveURL := ""
			if c, err := abccfg.Load(); err == nil {
				actx := c.ActiveCtx()
				abcWaveURL, _ = abccfg.GetAdminFloorField(&actx.Admin.Services, "wave", "http")
			}
			router := wave.NewRouter(abcWaveURL)
			override.WaveEndpoint = router.EndpointForAugment(cmd.Context())
			if router.IsAbcWaveConfigured() && override.WaveEndpoint == abcWaveURL {
				fmt.Fprintf(cmd.ErrOrStderr(), "  Wave: routing to abc-wave (%s)\n", override.WaveEndpoint)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "  Wave: abc-wave unavailable or unconfigured, routing to %s\n", override.WaveEndpoint)
			}
		}
	}

	spec := mergeSpec(base, override)

	// Mint the run tag BEFORE spec.defaults() so the bucket-layout
	// derivation below can use it. Spec defaults() sets spec.WorkDir to
	// "/work/nextflow-work" if still empty — that's the legacy fallback;
	// we want the user-rooted S3 default to take precedence on clusters
	// where the active context yields a group bucket.
	runTag := newRunTag()
	spec.RunTag = runTag

	// Bucket-layout defaults — auto-derive --work-dir and --outdir under
	// s3://<group-bucket>/<scope>/<user>/{workdir,results}/<run-tag>/ so
	// users stop inventing paths. Spec: abc-bucket-layout-phase-1.md.
	shareFlag, _ := cmd.Flags().GetBool("share")
	visFlag, _ := cmd.Flags().GetString("visibility")
	scope, scopeErr := resolveScope(visFlag, shareFlag)
	if scopeErr != nil {
		return scopeErr
	}
	workDirSource := "auto"
	outDirSource := "auto"
	if c, cfgErr := abccfg.Load(); cfgErr == nil {
		actx := c.ActiveCtx()
		groupBucket := strings.TrimSpace(actx.NomadNamespace())
		userSeg := ""
		if actx.Auth != nil {
			userSeg = utils.WhoamiPathSegment(actx.Auth.Whoami)
		}

		// Datacenter fallback: when neither --datacenter nor a saved-spec
		// value supplied a list, read contexts.<name>.admin.services.nomad.
		// datacenters. Operators set this once per context; users running
		// `abc pipeline run` no longer need --datacenter on every command.
		// CLI flag + saved spec still win when present.
		if len(spec.Datacenters) == 0 {
			if dcs := actx.NomadDatacenters(); len(dcs) > 0 {
				spec.Datacenters = dcs
			}
		}

		// Head- + worker-pool fallback. Resolution order:
		//   1. CLI flag (--head-pool / --worker-pool) — already on spec via override
		//   2. Active context (admin.services.nomad.{head_pool,worker_pool})
		//   3. Build-time defaults (platform / compute) — seedling-shaped
		// Build-time defaults are deliberately seedling-named; operators
		// running on a single-pool cluster should clear them by setting
		// admin.services.nomad.head_pool = "" via `abc config set`.
		if spec.HeadPool == "" {
			if hp := actx.NomadHeadPool(); hp != "" {
				spec.HeadPool = hp
			} else {
				spec.HeadPool = "platform"
			}
		}
		if spec.WorkerPool == "" {
			if wp := actx.NomadWorkerPool(); wp != "" {
				spec.WorkerPool = wp
			} else {
				spec.WorkerPool = "compute"
			}
		}
		if spec.WorkDir == "" && groupBucket != "" && userSeg != "" {
			spec.WorkDir = derivedWorkDir(groupBucket, scope, userSeg, runTag)
		} else if spec.WorkDir != "" {
			workDirSource = "user-set"
			if w, _ := validateExplicitWorkDir(spec.WorkDir, groupBucket); w != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "  WARNING: %s\n", w)
			}
		}
		if groupBucket != "" && userSeg != "" {
			if spec.Params == nil {
				spec.Params = map[string]any{}
			}
			if _, ok := spec.Params["outdir"]; ok {
				outDirSource = "user-set"
			} else {
				spec.Params["outdir"] = derivedOutDir(groupBucket, scope, userSeg, runTag)
			}
		}
	}

	spec.defaults()

	// Translate secret://name param values to Nomad template refs for abc-nodes.
	translateSecretParams(spec)

	// Resolve the dev plugin bundle (if requested) into a concrete artifact URL
	// and a default plugin set. Done after mergeSpec/defaults so a saved spec
	// can override individual plugin versions / pin a custom bundle URL while
	// still benefitting from --dev-plugins as a one-flag opt-in.
	if spec.DevPlugins {
		if spec.PluginBundleURL == "" {
			url, err := resolveDevPluginBundle()
			if err != nil {
				return err
			}
			spec.PluginBundleURL = url
		}
		if len(spec.Plugins) == 0 {
			spec.Plugins = defaultDevPlugins()
		}
		if len(spec.ExtraBinaries) == 0 {
			spec.ExtraBinaries = defaultDevPluginBinaries()
		}
	}

	// Cluster-baseline plugins — every pipeline launched here needs nf-nomad
	// (the executor) and, on seedling-prod, nf-nomad-s5cmd (work-dir over S3).
	// Merge defaultClusterPlugins() AFTER any --plugin / saved-spec entries so
	// operator overrides win on a per-ID basis. Skipped entirely under
	// --dev-plugins, which already replaced spec.Plugins with the 99.99.99 set.
	if !spec.DevPlugins {
		pinned := map[string]bool{}
		for _, p := range spec.Plugins {
			pinned[p.ID] = true
		}
		for _, p := range defaultClusterPlugins(spec.NfPluginVersion) {
			if !pinned[p.ID] {
				spec.Plugins = append(spec.Plugins, p)
			}
		}
	}

	// Guard against a versioned/bare plugin MIX before we submit a doomed job.
	//
	// Nextflow resolves a bare `id "<plugin>"` to the newest published release
	// ONLY when every plugin in the block is bare. As soon as ANY entry carries
	// an explicit @version, Nextflow stops auto-resolving the bare ones and the
	// head fails at startup with `Unknown Nextflow plugin '<id>'` — after the
	// head job has already been placed and pulled. Pinning versions is a normal,
	// supported thing to do (--plugin id@ver), so we don't
	// forbid it; we require CONSISTENCY: pin all baseline plugins or pin none.
	if err := validatePluginVersionConsistency(spec.Plugins); err != nil {
		return err
	}

	// Plugin-driven binary requirements — auto-add cluster tool binaries when
	// the corresponding plugin is loaded, regardless of --dev-plugins. The
	// head bootstrap shells out to these and the worker bootstrap probes
	// PATH for them; without an artifact stanza pulling them into local/bin
	// the head fails with "<tool>: command not found".
	//
	// Operators can still pin custom binaries via spec.ExtraBinaries; we
	// only append, never replace.
	pluginToBinary := map[string]string{
		"nf-nomad-s5cmd": "s5cmd",
		"nf-rclone":      "rclone",
	}
	have := map[string]bool{}
	for _, b := range spec.ExtraBinaries {
		have[b] = true
	}
	for _, p := range spec.Plugins {
		if bin, ok := pluginToBinary[p.ID]; ok && !have[bin] {
			spec.ExtraBinaries = append(spec.ExtraBinaries, bin)
			have[bin] = true
		}
	}

	if spec.DevNextflow {
		if spec.NextflowBinURL == "" {
			url, err := resolveDevNextflow()
			if err != nil {
				return err
			}
			spec.NextflowBinURL = url
		}
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Read Nomad connection details for embedding in the head job env block.
	nomadAddr, nomadToken := nomadConnFromCmd(cmd)

	runUUID := newRunUUID()

	// Mint a short alphanumeric *run tag* that gets used as the prefix on
	// both the head Nomad job-id and every child Nomad job-id. This is an
	// abc-cluster-cli orchestration concern only — distinct from Nextflow's
	// internal session.uniqueId. The tag is plumbed to nf-nomad via the
	// `NF_NOMAD_RUN_TAG` env var on the head task; nf-nomad's
	// NomadHelper.childJobName prefers this over its session-derived
	// fallback. Single-prefix correlation:
	//   nomad job status -prefix nf-<run-tag>-     → head + every worker
	// runTag + bucket-layout derivation moved upstream (before spec.defaults).

	// Pipeline slug leads the head job-id and every child job-id. `--name`
	// (user override) takes precedence over the auto-derived slug so a user
	// can pin a specific identifier (e.g. when running multiple variants of
	// the same pipeline).
	slug := strings.TrimSpace(spec.Name)
	if slug == "" {
		slug = pipelineSlug(spec.Repository)
	}
	if slug == "" {
		slug = "nextflow"
	}
	spec.PipelineSlug = slug

	// Head job-id = `<run-tag>-nf-head-<slug>`. Child job-ids (built by
	// nf-nomad's NomadHelper from NF_NOMAD_RUN_TAG) follow
	// `<run-tag>-<8task>-<process>`. Pipeline slug appears only on the
	// head — children get pipeline context from the process name itself
	// (`NFCORE_DEMO_DEMO_FASTQC` etc).
	//
	// Single-prefix correlation:
	//   nomad job status -prefix <run-tag>-      → head + every worker
	spec.Name = headJobName(runTag, slug)

	// Resume revision auto-discovery: on `--resume` against a reused --work-dir,
	// when the user did NOT explicitly pass --revision, reuse the revision the
	// prior run of that work-dir used. A resume only hits the cloudcache if it
	// runs byte-identical pipeline code, so it must use the same revision — and
	// making the user look that up (and pass the full SHA) was the sharpest edge
	// of the resume UX. Best-effort: any failure leaves spec.Revision untouched.
	if spec.Resume && !cmd.Flags().Changed("revision") {
		if root := pipelineWorkdirRoot(spec.WorkDir); root != "" {
			if db, err := state.Open(); err == nil {
				prior, ok, e := state.LatestRunForWorkdirRoot(cmd.Context(), db, root)
				db.Close()
				if e == nil && ok && prior.WorkloadVersion.Valid && prior.WorkloadVersion.String != "" {
					spec.Revision = prior.WorkloadVersion.String
					fmt.Fprintf(cmd.ErrOrStderr(),
						"↻ resume: reusing revision %s from prior run %s (pass --revision to override)\n",
						spec.Revision, prior.RunID)
				}
			}
		}
	}

	// Resume lineage run-name: name a resumed run `<base>_<n>` where <base> is
	// the original run's tag (parsed from the reused --work-dir) and <n> is its
	// position in the lineage (count of prior runs recorded against the same
	// workdir_root). Nextflow reuses the session UUID on resume, so the cache
	// can't self-count — local run records are the counter. Best-effort: any
	// failure (no db, non-canonical workdir) leaves NextflowRunName empty and
	// the generator falls back to the fresh run tag, which is always valid.
	if spec.Resume {
		if base := workDirRunTag(spec.WorkDir); base != "" {
			n := 1
			if db, err := state.Open(); err == nil {
				if c, e := state.CountRunsForWorkdirRoot(cmd.Context(), db, pipelineWorkdirRoot(spec.WorkDir)); e == nil && c >= 1 {
					n = c
				}
				db.Close()
			}
			spec.NextflowRunName = fmt.Sprintf("%s_%d", base, n)
		}
	}

	// Pin a Nextflow session UUID for cloudcache runs so the head is restart/
	// reschedule-resilient AND so `--work-dir` resumes actually reuse the cache:
	// the static entrypoint always re-runs `-resume <uuid>`, and the cloudcache
	// is keyed `cache/<run-tag>/<session-uuid>/`.
	//
	//   - explicit --session-id  → pin exactly that (caller knows the session).
	//   - everything else (fresh run OR `--resume` without --session-id) → pin a
	//     UUID DERIVED from the work-dir. A fresh run and every resume of it
	//     share the same canonical work-dir, hence the same UUID and the same
	//     cache namespace — so resume picks up completed tasks. (Previously this
	//     branch minted a fresh RANDOM UUID, so a resume always hit an empty
	//     namespace and re-ran every task.)
	//
	// Gated on a canonical S3 work-dir because `-resume` on a fresh head
	// container needs NXF_IGNORE_RESUME_HISTORY, which is only set when
	// NXF_CLOUDCACHE_PATH applies (same gate as deriveCloudCachePath).
	if deriveCloudCachePath(spec.WorkDir) != "" {
		if spec.Resume && spec.SessionID != "" {
			spec.PinnedSessionUUID = spec.SessionID
		} else {
			spec.PinnedSessionUUID = deterministicSessionUUID(spec.WorkDir)
		}
	}

	hcl := generateHeadJobHCL(spec, nomadAddr, nomadToken, runUUID)

	if dryRun {
		fmt.Fprint(cmd.OutOrStdout(), hcl)
		return nil
	}

	// Auto-attach to active project / investigation (spec abc-investigation §E).
	runID := autoAttachPipelineRun(cmd, spec)

	// Banner context for the bucket-layout lines printed by submitAndWatch.
	bannerCtx := bucketLayoutBanner{
		Scope:         scope,
		WorkDirSource: workDirSource,
		OutDirSource:  outDirSource,
	}
	if err := submitAndWatch(cmd.Context(), cmd, nc, spec, hcl, bannerCtx); err != nil {
		return err
	}
	// Spawn the run-watcher to write back completion fields. spec §E + OQ-6.
	// Best-effort: if the CLI exits before the alloc terminates, the row
	// stays at status='running'. Watcher uses its own DB handle.
	if runID != "" {
		runner.Watch(nomadAddr, nomadToken, "",
			runner.WatchTarget{RunID: runID, JobID: spec.Name, Namespace: spec.Namespace},
			runner.Config{}, cmd.ErrOrStderr())
	}
	return nil
}

// resolveSubmissionSource picks the right submission_source value for this
// invocation. Pipelines do not yet support --template / `pipeline rerun`,
// so the only signal today is ABC_CLI_AUTOMATION=1 vs. handwritten. The helper
// keeps the call site identical to job/module and ready for the rerun /
// template work that's pending.
func resolveSubmissionSource(cmd *cobra.Command) string {
	templateID, _ := cmd.Flags().GetString("template")
	rerun, _ := cmd.Flags().GetBool("rerun")
	return state.SubmissionSourceClassifier{
		TemplateID: templateID,
		Rerun:      rerun,
	}.Resolve()
}

// autoAttachPipelineRun resolves project/investigation per the precedence in
// spec abc-investigation §E and inserts a row into ~/.abc/local.db runs.
// Best-effort: failures only log a warning and never block the submit.
// Returns the runID so the caller can spawn the run-watcher post-submit.
func autoAttachPipelineRun(cmd *cobra.Command, spec *PipelineSpec) string {
	noProj, _ := cmd.Flags().GetBool("no-project")
	noInv, _ := cmd.Flags().GetBool("no-investigation")
	pflag, _ := cmd.Flags().GetString("project")
	iflag, _ := cmd.Flags().GetString("investigation")

	db, err := state.Open()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "[abc] auto-attach: state DB unavailable (%v); skipping run record\n", err)
		return ""
	}
	contextName := state.ActiveContextName()
	scratchGB, _ := cmd.Flags().GetFloat64("scratch-gb")
	// CPU request: pipeline spec carries MHz; convert to cores so the
	// runs.cpu_request column is consistent across verbs (job uses
	// cores natively). 1000 MHz ≈ 1 core under our defaults.
	var cpuReqCores float64
	if spec.CPU > 0 {
		cpuReqCores = float64(spec.CPU) / 1000.0
	}
	var memReqGB float64
	if spec.MemoryMB > 0 {
		memReqGB = float64(spec.MemoryMB) / 1024.0
	}
	req := state.AutoAttachRequest{
		ContextName:       contextName,
		NoProject:         noProj,
		NoInvestigation:   noInv,
		ProjectFlag:       pflag,
		InvestigationFlag: iflag,
		WorkloadRef:       spec.Repository,
		WorkloadVersion:   spec.Revision,
		Verb:              "pipeline",
		Namespace:         spec.Namespace,
		ScratchGB:         scratchGB,
		CPURequest:        cpuReqCores,
		MemRequestGB:      memReqGB,
		SubmissionSource:  resolveSubmissionSource(cmd),
	}
	res, err := state.AutoAttachAndInsertRun(cmd.Context(), db, cmd.ErrOrStderr(), req)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "[abc] auto-attach: %v (continuing without run record)\n", err)
		return ""
	}
	// Stash the workdir root so the future Nextflow-cache aggregator
	// (brainstorm: brainstorms/abc-report-use-cases/2026-05-27-…) can
	// join per-task cost rows across resumes that share this prefix.
	// Soft-fail: if the column isn't there or the write breaks, the
	// run record stays intact — the aggregator just won't see this row.
	if root := pipelineWorkdirRoot(spec.WorkDir); root != "" {
		if err := state.UpdateRunWorkdirRoot(cmd.Context(), db, res.RunID, root); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "[abc] auto-attach: workdir_root write failed: %v\n", err)
		}
	}
	return res.RunID
}

// pipelineWorkdirRoot returns the stable workdir prefix shared across
// resumes for a canonical CLI-derived workdir path. Examples:
//
//	s3://su-demo/user/slate-sunbird/workdir/slate-sunbird-1779…/  → same
//	s3://su-demo/user/slate-sunbird/workdir/slate-sunbird-1779…   → same (trailing / normalised)
//	/scratch/local/nf-work/foo/                                    → "" (operator path, no resume model)
//	""                                                              → ""
//
// Resume-as-the-same-pipeline means re-using the same --work-dir, so
// the path itself is the resume key. We normalise the trailing slash
// so two writes (one with /, one without) collide on the same root
// in the aggregator's GROUP BY.
func pipelineWorkdirRoot(workdir string) string {
	if !strings.HasPrefix(workdir, "s3://") {
		return ""
	}
	return strings.TrimRight(workdir, "/") + "/"
}

// workDirRunTag extracts the original run tag from a canonical CLI-derived
// S3 work-dir — the segment after ".../workdir/". It is the base name a
// resume lineage numbers from. Returns "" for non-canonical / non-S3 paths
// (operator-supplied work dirs don't carry a resume lineage).
//
//	s3://su-demo/user/slate-sunbird/workdir/slate-sunbird-1779.../  → slate-sunbird-1779...
//	s3://b/x/workdir/                                               → ""
//	/scratch/nf-work/foo/                                          → ""
func workDirRunTag(workdir string) string {
	if !strings.HasPrefix(workdir, "s3://") {
		return ""
	}
	parts := strings.Split(strings.TrimRight(strings.TrimPrefix(workdir, "s3://"), "/"), "/")
	// Canonical shape: <bucket>/<scope>/<user>/workdir/<run-tag> (≥5 parts);
	// the run tag is the segment immediately after the literal "workdir".
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "workdir" && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return ""
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

// bucketLayoutBanner carries the resolved scope + source labels needed to
// render the submit-time banner. Built once by the caller (where the
// active context + flags are available); passed into submitAndWatch so
// the banner stays at the existing print site.
type bucketLayoutBanner struct {
	Scope         string // "user" or "group"
	WorkDirSource string // "auto" or "user-set"
	OutDirSource  string // "auto" or "user-set"
}

func submitAndWatch(ctx context.Context, cmd *cobra.Command, nc *utils.NomadClient, spec *PipelineSpec, hcl string, banner bucketLayoutBanner) error {
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

	if err := nc.PreflightJobDriverPolicy(ctx, jobJSON, cmd.ErrOrStderr()); err != nil {
		log.LogAttrs(ctx, debuglog.L1, "pipeline.run.failed",
			debuglog.AttrsError("pipeline.driver_policy", err)...,
		)
		return err
	}
	if err := nc.PreflightJobTaskDrivers(ctx, jobJSON, cmd.ErrOrStderr()); err != nil {
		log.LogAttrs(ctx, debuglog.L1, "pipeline.run.failed",
			debuglog.AttrsError("pipeline.driver_preflight", err)...,
		)
		return err
	}

	// Display-only job ID for the "Pipeline submitted" banner. Must match
	// the actual job ID embedded in jobJSON (set via headJobName() at the
	// top of this function — runTag already encodes the submitter's whoami
	// slug, so the slug is implicit). Historical code re-prepended the slug
	// here, which doubled the prefix in the user-facing output (e.g.
	// "slate-slate-sunbird-..." vs the real "slate-sunbird-..."); fixed
	// 2026-05-26 — relying on spec.Name alone keeps display ↔ Nomad in sync.
	jobName := "nextflow-head"
	if spec.Name != "" {
		jobName = spec.Name
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
	// Bucket-layout banner lines — spec abc-bucket-layout-phase-1.md.
	// Always print so users learn the layout (and notice when their
	// explicit path differs from the auto-default).
	if spec.WorkDir != "" {
		fmt.Fprintf(out, "  Workdir    %s [%s]\n", spec.WorkDir, banner.WorkDirSource)
	}
	if od, ok := spec.Params["outdir"]; ok && od != nil {
		if odStr := fmt.Sprintf("%v", od); odStr != "" {
			fmt.Fprintf(out, "  Results    %s [%s]\n", odStr, banner.OutDirSource)
		}
	}
	switch banner.Scope {
	case scopeGroup:
		fmt.Fprintf(out, "  Visibility group-readable (--share / --visibility=group)\n")
	case scopeUser:
		// Only mention the --share hint when we actually auto-derived (no
		// point teaching the flag when the user explicitly set their path).
		if banner.WorkDirSource == "auto" {
			fmt.Fprintf(out, "  Visibility user-private (use --share for group-visible)\n")
		} else {
			fmt.Fprintf(out, "  Visibility user-private\n")
		}
	}
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

// newSessionUUID returns a random RFC-4122-shaped UUID string (8-4-4-4-12),
// the form Nextflow's `-resume <uuid>` requires (java.util.UUID.fromString).
// Used to pin a fresh run's session so the head can resume itself on a Nomad
// restart/reschedule.
func newSessionUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("00000000-0000-0000-0000-%012x", os.Getpid()&0xffffffffffff)
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// deterministicSessionUUID derives a STABLE RFC-4122-shaped UUID (8-4-4-4-12,
// the form `-resume <uuid>` requires) from the run's canonical cloudcache path.
//
// Why this exists: nf-cloudcache keys the cache by session UUID —
// `cache/<run-tag>/<session-uuid>/`. A fresh run and every later `--work-dir`
// resume of it resolve to the SAME cloudcache path (deriveCloudCachePath is a
// pure function of the work-dir), so deriving the session UUID from that path
// makes them pin the SAME session and share one cache namespace. `-resume`
// then actually reuses completed tasks. Previously a resume without an explicit
// --session-id minted a fresh RANDOM UUID, landed on an empty namespace, and
// re-ran every task — defeating resume entirely.
//
// Falls back to a random UUID for non-canonical work dirs (deriveCloudCachePath
// == ""), where no cloudcache applies and the value is unused for resume.
func deterministicSessionUUID(workDir string) string {
	key := deriveCloudCachePath(workDir)
	if key == "" {
		return newSessionUUID()
	}
	h := sha1.Sum([]byte("abc-nf-session:v1:" + key))
	b := h[:16]
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newRunTag returns a fresh run tag — the shared prefix on the head Nomad
// job-id and every child Nomad job-id (single-prefix correlation).
//
// Default form: `<sanitized-whoami>-<unix-seconds>` (e.g.
// `abhi-admin-1730568000`). Using the submitter's whoami makes job
// listings legible at a glance; the monotonic Unix-seconds suffix
// guarantees distinct tags between successive submissions and gives ops a
// trivially parseable timestamp (`date -d @1730568000`).
//
// When whoami is unavailable (no config, fresh install, etc.) the form
// is `nf-<unix-seconds>` — `nf-` keeps the cluster marker visible.
//
// Collision risk: only when the same user submits twice within the same
// wall-second. That surfaces as a Nomad "job already exists" submit error,
// not data corruption — the user resubmits a moment later. For
// finer-grained protection (sub-second submits via scripted dispatch),
// the API caller can override RunTag explicitly.
//
// Length budget: e.g. an 18-char whoami + `-` + 10-digit timestamp =
// 29 chars; the head job-id (`<runtag>-nf-head-<slug>`) and child
// job-id (`<runtag>-<8task>-<process>`) both stay well within Nomad's
// 128-char job-id soft limit.
func newRunTag() string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	whoami := activeWhoamiTag()
	if whoami != "" {
		return whoami + "-" + ts
	}
	return "nf-" + ts
}

// activeWhoamiTag returns the active context's whoami, sanitized for use
// in a Nomad job-id (lowercase, alphanumeric + dashes, no leading/trailing
// dash, capped at a sensible length to leave room for slug + task hash).
// Returns "" when whoami is unavailable.
func activeWhoamiTag() string {
	cfg, err := abccfg.Load()
	if err != nil || cfg == nil {
		return ""
	}
	ctx := cfg.ActiveCtx()
	raw := ""
	if v := strings.TrimSpace(ctx.Admin.Whoami); v != "" {
		raw = v
	} else if ctx.Auth != nil {
		raw = strings.TrimSpace(ctx.Auth.Whoami)
	}
	if raw == "" {
		return ""
	}
	// Use rightmost colon segment for "scope:role" style whoami values.
	if i := strings.LastIndex(raw, ":"); i >= 0 {
		raw = raw[i+1:]
	}
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	const maxWhoamiLen = 24
	if len(out) > maxWhoamiLen {
		out = strings.TrimRight(out[:maxWhoamiLen], "-")
	}
	return out
}

// pipelineSlug derives a short, lowercase, dash-joined slug from a pipeline
// repository spec. Examples:
//
//	nf-core/demo                     → nfcore-demo
//	https://github.com/abhi/foo.git  → abhi-foo
//	../local/path/to/main.nf         → main
//
// Truncated to 40 chars; on overflow the tail is replaced with a 7-char
// SHA1-derived suffix so distinct long names stay distinguishable.
func pipelineSlug(repo string) string {
	s := strings.TrimSpace(repo)
	if s == "" {
		return ""
	}
	// Strip URL scheme + host to expose `<owner>/<repo>` for github/gitlab URLs.
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if j := strings.Index(s, "/"); j >= 0 {
			s = s[j+1:]
		}
	}
	// Drop common Git suffixes and Nextflow main script noise.
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/main.nf")
	// Lowercase everything; replace path separators + non-alnum with single dash.
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	const slugMax = 40
	if len(out) <= slugMax {
		return out
	}
	// Long: keep the head and append a 7-char SHA1-derived suffix.
	sum := sha1.Sum([]byte(repo))
	return out[:slugMax-8] + "-" + hex.EncodeToString(sum[:4])[:7]
}

// headJobName assembles the head job-id in the
// `<run-tag>-nf-head-<slug>` form. The run-tag leads so it shares a prefix
// with every child job-id (`<run-tag>-<8task>-<process>` from
// NomadHelper.childJobName); the trailing slug is human-readable context
// for the head row in `nomad job status` output.
//
// Single-prefix correlation:
//
//	nomad job status -prefix <run-tag>-     → head + every worker
//
// Capped at 128 chars (Nomad job-id soft limit); on overflow the slug is
// truncated and a 7-char SHA1 suffix appended.
func headJobName(runTag, slug string) string {
	const max = 128
	prefix := runTag + "-nf-head-"
	budget := max - len(prefix)
	if len(slug) <= budget {
		return prefix + slug
	}
	headLen := budget - 8
	if headLen < 1 {
		headLen = 1
	}
	sum := sha1.Sum([]byte(slug))
	return prefix + slug[:headLen] + "-" + hex.EncodeToString(sum[:4])[:7]
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
