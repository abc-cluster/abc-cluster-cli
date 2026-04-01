// Package job implements the "abc job" command group, including "abc job run"
// which parses preamble directives and generates a Nomad HCL batch job spec.
package job

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <script>",
		Short: "Generate (or submit) a Nomad HCL batch job from an annotated script",
		Long: `Parse #ABC/#NOMAD preamble directives from a script and produce a Nomad
HCL job spec. Without --submit the HCL is printed to stdout; with --submit it
is registered directly with Nomad.

DIRECTIVE PRECEDENCE (highest → lowest)
  CLI flags  >  #ABC preamble  >  #NOMAD preamble  >  NOMAD_* env vars  >  params file

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CLASS 1 — SCHEDULER  (configure HCL stanza fields)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  --name=<string>              Job name (default: script filename stem)
  --region=<string>            Nomad region
  --namespace=<string>         Nomad namespace
  --dc=<string>                Target datacenter (repeatable)
  --priority=<1-100>           Scheduler priority (default: 50)
  --nodes=<int>                Parallel group instances / array width (default: 1)
  --cores=<int>                CPU cores per task
  --mem=<size>[K|M|G]          Memory per task (e.g. 4G, 512M)
  --gpus=<int>                 GPU count (nvidia/gpu device plugin)
  --time=<HH:MM:SS>            Walltime limit; wraps command in timeout(1)
  --chdir=<path>               Working directory inside task sandbox
  --driver=<string>            Task driver: exec (default), hpc-bridge, docker
  --depend=<complete:job-id>   Block on another job via prestart lifecycle hook
  --output=<filename>          Tee stdout to $NOMAD_TASK_DIR/<filename>
  --error=<filename>           Tee stderr to $NOMAD_TASK_DIR/<filename>
  --constraint=<attr><op><val> Nomad placement constraint (repeatable)
                               Ops: == != =~ !~ < <= > >=
                               Example: --constraint=region==za-cpt
  --affinity=<expr>[,weight=N] Nomad placement affinity (repeatable)
                               Example: --affinity=datacenter==c1,weight=75
  --no-network                 Disable network access (Nomad mode = "none")
  --port=<label>               Dynamic port; injects NOMAD_IP/PORT/ADDR_<label>
  --driver.config.<key>=<val>  Pass arbitrary driver config fields

RESCHEDULE  (stored as abc_reschedule_* meta keys)
  --reschedule-mode=<delay|fail>
  --reschedule-attempts=<int>
  --reschedule-interval=<duration>   e.g. 30s
  --reschedule-delay=<duration>      e.g. 5s
  --reschedule-max-delay=<duration>  e.g. 1m

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CLASS 2 — RUNTIME EXPOSURE  (inject NOMAD_* vars into task env block)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Note: NOMAD_REGION is always injected by Nomad automatically.
PBS_* and SLURM_* compatibility aliases are always emitted.

  Task identity
    --alloc_id         NOMAD_ALLOC_ID        (unique per execution)
    --short_alloc_id   NOMAD_SHORT_ALLOC_ID
    --alloc_name       NOMAD_ALLOC_NAME      (<job>.<group>[<index>])
    --alloc_index      NOMAD_ALLOC_INDEX     (0-based; use to shard array jobs)
    --job_id           NOMAD_JOB_ID
    --job_name         NOMAD_JOB_NAME
    --parent_job_id    NOMAD_JOB_PARENT_ID   (dispatched jobs only)
    --group_name       NOMAD_GROUP_NAME
    --task_name        NOMAD_TASK_NAME
    --namespace        NOMAD_NAMESPACE       (env exposure; --namespace=<n> for placement)
    --dc               NOMAD_DC              (env exposure; --dc=<n> for placement)

  Resources
    --cpu_limit        NOMAD_CPU_LIMIT       (MHz)
    --cpu_cores        NOMAD_CPU_CORES       (use for -t in BWA/samtools/STAR)
    --mem_limit        NOMAD_MEMORY_LIMIT    (MB; use for JVM -Xmx)
    --mem_max_limit    NOMAD_MEMORY_MAX_LIMIT

  Directories
    --alloc_dir        NOMAD_ALLOC_DIR       (shared across the group)
    --task_dir         NOMAD_TASK_DIR        (per-task private scratch)
    --secrets_dir      NOMAD_SECRETS_DIR     (in-memory, noexec)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CLASS 3 — META  (Nomad meta block → NOMAD_META_<KEY> in the task)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  --meta=<key>=<value>         Repeatable. Key is uppercased for env access.
                               Example: --meta=sample_id=S001
                                        → NOMAD_META_SAMPLE_ID=S001

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
PARAMS FILE  (YAML, lowest priority after env vars)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  --params-file=<path>         YAML file with directive key/value pairs.
                               Nested keys are dot-flattened:
                                 cores: 8  →  --cores=8
                                 meta:
                                   sample: S001  →  --meta=sample=S001

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
INLINE COMMENTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Trailing shell comments are stripped from #ABC/#NOMAD lines, so
  annotated preambles are valid:
    #ABC --cores=8    # 8 cores per task (same as SLURM --cpus-per-task)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
EXAMPLES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  # Print generated HCL (no cluster needed)
  abc job run bwa-align.sh

  # Pipe to nomad directly
  abc job run bwa-align.sh | nomad job run -

  # Dry-run: plan server-side, show placement feasibility
  abc job run bwa-align.sh --dry-run --region za-cpt

  # Submit and tail logs immediately
  abc job run bwa-align.sh --submit --region za-cpt --watch

  # Override preamble directive from CLI
  abc job run bwa-align.sh --submit --nodes=96 --cores=16

  # Write HCL to file instead of stdout
  abc job run bwa-align.sh --output-file bwa-align.hcl`,
		Args: cobra.ExactArgs(1),
		RunE: runJob,
	}

	// Submission modes
	cmd.Flags().Bool("submit", false, "Submit job to Nomad instead of printing HCL")
	cmd.Flags().Bool("dry-run", false, "Plan job server-side without submitting")
	cmd.Flags().Bool("watch", false, "Stream logs after --submit")
	cmd.Flags().String("output-file", "", "Write generated HCL to file instead of stdout")

	// Scheduler overrides (mirror preamble Class 1)
	cmd.Flags().String("name", "", "Job name")
	cmd.Flags().String("namespace", "", "Nomad namespace")
	cmd.Flags().String("region", "", "Nomad region")
	cmd.Flags().StringSlice("dc", nil, "Target datacenter(s)")
	cmd.Flags().Int("priority", 0, "Scheduler priority (1-100)")
	cmd.Flags().Int("nodes", 0, "Number of group instances")
	cmd.Flags().Int("cores", 0, "CPU cores per task")
	cmd.Flags().String("mem", "", "Memory per task (e.g. 8G, 512M)")
	cmd.Flags().Int("gpus", 0, "GPU count")
	cmd.Flags().String("time", "", "Walltime limit HH:MM:SS")
	cmd.Flags().String("chdir", "", "Working directory inside task sandbox")
	cmd.Flags().String("depend", "", "Dependency spec (complete:<job-id>)")
	cmd.Flags().String("driver", "", "Task driver (exec, hpc-bridge, docker)")
	cmd.Flags().String("output", "", "Tee stdout to $NOMAD_TASK_DIR/<filename>")
	cmd.Flags().String("error", "", "Tee stderr to $NOMAD_TASK_DIR/<filename>")
	cmd.Flags().Bool("no-network", false, "Disable network access for this job")
	cmd.Flags().StringSlice("port", nil, "Named network ports")

	// Reschedule
	cmd.Flags().String("reschedule-mode", "", "Reschedule mode (delay, fail)")
	cmd.Flags().Int("reschedule-attempts", 0, "Max reschedule attempts")
	cmd.Flags().String("reschedule-interval", "", "Reschedule interval (e.g. 30s)")
	cmd.Flags().String("reschedule-delay", "", "Reschedule base delay (e.g. 5s)")
	cmd.Flags().String("reschedule-max-delay", "", "Reschedule max delay (e.g. 1m)")

	// Meta + params
	cmd.Flags().StringToString("meta", nil, "Meta key=value (repeatable)")
	cmd.Flags().String("params-file", "", "YAML params file path")

	return cmd
}

func applyCLIFlags(cmd *cobra.Command, spec *jobSpec) error {
	if v, _ := cmd.Flags().GetString("name"); v != "" {
		spec.Name = v
	}
	if v, _ := cmd.Flags().GetString("namespace"); v != "" {
		spec.Namespace = v
	}
	if v, _ := cmd.Flags().GetString("region"); v != "" {
		spec.Region = v
	}
	if v, _ := cmd.Flags().GetStringSlice("dc"); len(v) > 0 {
		spec.Datacenters = v
	}
	if v, _ := cmd.Flags().GetInt("priority"); v != 0 {
		spec.Priority = v
	}
	if v, _ := cmd.Flags().GetInt("nodes"); v != 0 {
		spec.Nodes = v
	}
	if v, _ := cmd.Flags().GetInt("cores"); v != 0 {
		spec.Cores = v
	}
	if v, _ := cmd.Flags().GetString("mem"); v != "" {
		mb, err := parseMemoryMB(v)
		if err != nil {
			return err
		}
		spec.MemoryMB = mb
	}
	if v, _ := cmd.Flags().GetInt("gpus"); v != 0 {
		spec.GPUs = v
	}
	if v, _ := cmd.Flags().GetString("time"); v != "" {
		secs, err := walltimeToSeconds(v)
		if err != nil {
			return err
		}
		spec.WalltimeSecs = secs
	}
	if v, _ := cmd.Flags().GetString("chdir"); v != "" {
		spec.ChDir = v
	}
	if v, _ := cmd.Flags().GetString("depend"); v != "" {
		spec.Depend = v
	}
	if v, _ := cmd.Flags().GetString("driver"); v != "" {
		spec.Driver = v
	}
	if v, _ := cmd.Flags().GetString("reschedule-mode"); v != "" {
		spec.RescheduleMode = v
	}
	if v, _ := cmd.Flags().GetInt("reschedule-attempts"); v != 0 {
		spec.RescheduleAttempts = v
	}
	if v, _ := cmd.Flags().GetString("reschedule-interval"); v != "" {
		spec.RescheduleInterval = v
	}
	if v, _ := cmd.Flags().GetString("reschedule-delay"); v != "" {
		spec.RescheduleDelay = v
	}
	if v, _ := cmd.Flags().GetString("reschedule-max-delay"); v != "" {
		spec.RescheduleMaxDelay = v
	}
	if v, _ := cmd.Flags().GetString("output"); v != "" {
		spec.OutputLog = v
	}
	if v, _ := cmd.Flags().GetString("error"); v != "" {
		spec.ErrorLog = v
	}
	if m, _ := cmd.Flags().GetStringToString("meta"); len(m) > 0 {
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		for k, v := range m {
			spec.Meta[k] = v
		}
	}
	if ps, _ := cmd.Flags().GetStringSlice("port"); len(ps) > 0 {
		spec.Ports = ps
	}
	if v, _ := cmd.Flags().GetBool("no-network"); v {
		spec.NoNetwork = true
	}
	return nil
}

func runJob(cmd *cobra.Command, args []string) error {
	scriptPath := args[0]

	f, err := os.Open(scriptPath)
	if err != nil {
		return fmt.Errorf("cannot open script %q: %w", scriptPath, err)
	}
	defer f.Close()

	scriptBytes, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("cannot read script %q: %w", scriptPath, err)
	}

	abcDirs, nomadDirs, err := parsePreamble(bytes.NewReader(scriptBytes))
	if err != nil {
		return fmt.Errorf("failed to parse script preamble: %w", err)
	}

	scriptBase := filepath.Base(scriptPath)
	defaultName := strings.TrimSuffix(scriptBase, filepath.Ext(scriptBase))

	scriptSpec, err := resolveSpec(abcDirs, nomadDirs, defaultName)
	if err != nil {
		return err
	}

	// Env vars override script preamble; CLI flags override everything.
	spec := mergeSpec(scriptSpec, readNomadEnvVars())

	if paramsFile, _ := cmd.Flags().GetString("params-file"); paramsFile != "" {
		params, err := loadParamsFile(paramsFile)
		if err != nil {
			return err
		}
		paramsSpec := &jobSpec{}
		for _, p := range params {
			if err := applyDirective(paramsSpec, p, "PARAMS"); err != nil {
				return err
			}
		}
		spec = mergeSpec(spec, paramsSpec)
	}

	if err := applyCLIFlags(cmd, spec); err != nil {
		return err
	}

	// Stamp submission metadata into meta block.
	if spec.Meta == nil {
		spec.Meta = map[string]string{}
	}
	submissionID := newSubmissionID()
	spec.Meta["abc_submission_id"] = submissionID
	spec.Meta["abc_submission_time"] = time.Now().UTC().Format(time.RFC3339)
	if spec.Name != "" {
		base := spec.Name
		if !strings.HasPrefix(base, "script-job-") {
			base = "script-job-" + base
		}
		spec.Name = fmt.Sprintf("%s-%s", base, submissionID[:8])
	}

	hcl := generateHCL(spec, scriptBase, string(scriptBytes))

	submit, _ := cmd.Flags().GetBool("submit")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	outputFile, _ := cmd.Flags().GetString("output-file")

	if submit || dryRun {
		return runWithNomad(cmd.Context(), cmd, spec, hcl, submit, dryRun)
	}
	if outputFile != "" {
		return os.WriteFile(outputFile, []byte(hcl), 0644)
	}
	fmt.Fprint(cmd.OutOrStdout(), hcl)
	return nil
}

func runWithNomad(ctx context.Context, cmd *cobra.Command, spec *jobSpec, hcl string, submit, dryRun bool) error {
	nc := nomadClientFromCmd(cmd)

	fmt.Fprintf(cmd.ErrOrStderr(), "  Parsing HCL via Nomad (%s)...\n", nomadAddrFromCmd(cmd))
	jobJSON, err := nc.ParseHCL(ctx, hcl)
	if err != nil {
		return fmt.Errorf("nomad HCL parse: %w", err)
	}

	if dryRun {
		plan, err := nc.PlanJob(ctx, spec.Name, jobJSON)
		if err != nil {
			return fmt.Errorf("nomad plan: %w", err)
		}
		printPlan(cmd, hcl, plan)
		return nil
	}

	region := spec.Region
	if region == "" {
		region = "default"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "  Submitting to Nomad (%s)...\n", region)
	resp, err := nc.RegisterJob(ctx, jobJSON)
	if err != nil {
		return fmt.Errorf("nomad register: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  ✓ Job submitted\n")
	fmt.Fprintf(out, "  Nomad job ID   %s\n", spec.Name)
	fmt.Fprintf(out, "  Evaluation ID  %s\n", resp.EvalID)
	if resp.Warnings != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  ⚠ Warnings: %s\n", resp.Warnings)
	}

	if watch, _ := cmd.Flags().GetBool("watch"); watch {
		fmt.Fprintln(cmd.ErrOrStderr(), "\n  Waiting for allocation...")
		return watchJobLogs(ctx, nc, spec.Name, spec.Namespace, out, watchDelay, watchTimeout)
	}

	fmt.Fprintf(out, "\n  Track progress:\n")
	fmt.Fprintf(out, "    abc job logs %s --follow\n", spec.Name)
	fmt.Fprintf(out, "    abc job show %s\n", spec.Name)
	return nil
}

func printPlan(cmd *cobra.Command, hcl string, plan *NomadPlanResponse) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  --- GENERATED HCL ---\n%s  ---------------------\n\n", hcl)
	fmt.Fprintf(out, "  Diff type: %s\n", plan.Diff.Type)
	for tg, upd := range plan.Annotations.DesiredTGUpdates {
		fmt.Fprintf(out, "  Task group %q: place=%d update=%d stop=%d\n",
			tg, upd.Place, upd.Update, upd.Stop)
	}
	if len(plan.FailedTGAllocs) > 0 {
		fmt.Fprintf(out, "  ⚠ Failed placements: %d task group(s) could not be placed\n",
			len(plan.FailedTGAllocs))
	}
	if plan.Warnings != "" {
		fmt.Fprintf(out, "  Warnings: %s\n", plan.Warnings)
	}
	fmt.Fprintf(out, "\n  ✓ Dry-run complete. Use --submit to register.\n")
}

const (
	watchDelay   = 10 * time.Second
	watchTimeout = 5 * time.Minute
)

func watchJobLogs(ctx context.Context, nc *nomadClient, jobID, namespace string,
	w io.Writer, delay, timeout time.Duration) error {
	start := time.Now()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if timeout > 0 && time.Since(start) > timeout {
			return fmt.Errorf("watch timeout after %s", timeout)
		}

		allocs, err := nc.GetJobAllocs(ctx, jobID, namespace, false)
		if err != nil {
			return err
		}

		var chosen *NomadAllocStub
		for _, a := range allocs {
			if a.ClientStatus == "running" {
				chosen = &a
				break
			}
			if chosen == nil || a.CreateTime > chosen.CreateTime {
				chosen = &a
			}
		}

		if chosen != nil {
			task := "main"
			for t := range chosen.TaskStates {
				task = t
				break
			}
			follow := chosen.ClientStatus == "running"
			return nc.StreamLogs(ctx, chosen.ID, task, "stdout", "start", 0, follow, w)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-sleepCh(int(delay.Seconds())):
		}
	}
}

func newSubmissionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("sub-%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}
