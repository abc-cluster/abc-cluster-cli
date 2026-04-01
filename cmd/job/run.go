// Package job implements the "abc job" command group, including "abc job run"
// which parses preamble directives and generates a Nomad HCL batch job spec.
package job

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// nomadConstraint holds a simple Nomad constraint item.
type nomadConstraint struct {
	Attribute string
	Operator  string
	Value     string
}

// nomadAffinity holds a simple Nomad affinity item.
type nomadAffinity struct {
	Attribute string
	Operator  string
	Value     string
	Weight    int
}

// jobSpec holds the configuration for a Nomad batch job.
//
// Scheduler directives set placement fields (Region, Datacenters, Priority…).
// Runtime-exposure directives are boolean flags that inject NOMAD_* vars into
// the task env block so the script can read them at runtime.
// Meta directives pass key-value pairs through Nomad's meta block.
type jobSpec struct {
	// ── Scheduler directives ─────────────────────────────────────────────────
	Name         string
	Namespace    string
	Region       string
	Datacenters  []string
	Priority     int
	Nodes        int
	Cores        int
	MemoryMB     int
	GPUs         int
	WalltimeSecs int
	ChDir        string
	Depend       string
	Driver           string
	DriverConfig     map[string]string
	RescheduleMode   string
	RescheduleAttempts int
	RescheduleInterval string
	RescheduleDelay    string
	RescheduleMaxDelay string
	OutputLog        string
	ErrorLog         string
	NoNetwork        bool
	Constraints      []nomadConstraint
	Affinities       []nomadAffinity

	// ── Meta directives ───────────────────────────────────────────────────────
	Meta map[string]string

	// ── Network directives ────────────────────────────────────────────────────
	Ports []string

	// ── Runtime-exposure boolean flags ────────────────────────────────────────
	ExposeAllocID      bool
	ExposeShortAllocID bool
	ExposeAllocName    bool
	ExposeAllocIndex   bool
	ExposeJobID        bool
	ExposeJobName      bool
	ExposeParentJobID  bool
	ExposeGroupName    bool
	ExposeTaskName     bool
	ExposeNamespaceEnv bool
	ExposeDCEnv        bool
	ExposeCPULimit     bool
	ExposeCPUCores     bool
	ExposeMemLimit     bool
	ExposeMemMaxLimit  bool
	ExposeAllocDir     bool
	ExposeTaskDir      bool
	ExposeSecretsDir   bool
}

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <script>",
		Short: "Generate a Nomad HCL batch job spec from an annotated Bash script",
		Long: `Parse preamble directives from a Bash script and print a Nomad HCL job spec.

Directive sources (priority order, highest first):
  1. #ABC  preamble lines
  2. #NOMAD preamble lines
  3. NOMAD_* env vars at invocation time

DIRECTIVE CLASSES

Class 1 - Scheduler (configure HCL stanza fields):
  --name=<string>        Job name (default: script filename without extension)
  --region=<string>      Nomad region / jurisdiction boundary
  --namespace=<string>   Nomad namespace
  --dc=<string>          Restrict to datacenter (repeatable)
  --priority=<int>       Scheduler priority (default: 50)
  --nodes=<int>          Group instance count (default: 1)
  --cores=<int>          CPU cores per task
  --mem=<size>[K|M|G]    Memory per task
  --gpus=<int>           GPU count (nvidia/gpu device)
  --time=<HH:MM:SS>      Walltime limit
  --chdir=<path>         Working directory inside task sandbox
  --depend=<type:id>     Dependency on another job (prestart hook)
  --driver=<string>      Task driver: exec2 (default), hpc-bridge, docker

Class 2 - Runtime-exposure (inject NOMAD_* vars into task env block):
  --alloc_id             NOMAD_ALLOC_ID
  --short_alloc_id       NOMAD_SHORT_ALLOC_ID
  --alloc_name           NOMAD_ALLOC_NAME
  --alloc_index          NOMAD_ALLOC_INDEX  (use to shard array jobs)
  --job_id               NOMAD_JOB_ID
  --job_name             NOMAD_JOB_NAME
  --parent_job_id        NOMAD_JOB_PARENT_ID
  --group_name           NOMAD_GROUP_NAME
  --task_name            NOMAD_TASK_NAME
  --namespace            NOMAD_NAMESPACE  (env exposure only)
  --dc                   NOMAD_DC         (env exposure; --dc=<n> for placement)
  --cpu_limit            NOMAD_CPU_LIMIT
  --cpu_cores            NOMAD_CPU_CORES  (use to set -t for BWA/samtools/STAR)
  --mem_limit            NOMAD_MEMORY_LIMIT
  --mem_max_limit        NOMAD_MEMORY_MAX_LIMIT
  --alloc_dir            NOMAD_ALLOC_DIR  (shared across task group)
  --task_dir             NOMAD_TASK_DIR   (private per-task scratch)
  --secrets_dir          NOMAD_SECRETS_DIR

  Note: NOMAD_REGION is always injected automatically by Nomad.

Class 3 - Meta (Nomad meta block, readable as NOMAD_META_<KEY>):
  --meta=<key>=<value>   (repeatable)

Network:
  --port=<label>         Dynamic port; injects NOMAD_IP/PORT/ADDR_<label>
  --no-network          Disable all network access (nomad network mode = "none")

PRECEDENCE: #ABC > #NOMAD > NOMAD_* env vars
  --dc=<n>  sets scheduler placement
  bare --dc injects NOMAD_DC runtime var

EXAMPLES
  abc job run job.sh | nomad job run -
  abc job run bwa-align.sh --submit --region za-cpt --watch
  abc job run bwa-align.sh --dry-run --region za-cpt`,
		Args: cobra.ExactArgs(1),
		RunE: runJob,
	}
	cmd.Flags().String("name", "", "Job name")
	cmd.Flags().String("namespace", "", "Nomad namespace")
	cmd.Flags().String("region", "", "Nomad region")
	cmd.Flags().StringSlice("dc", nil, "Target datacenter(s)")
	cmd.Flags().Int("priority", 0, "Scheduler priority")
	cmd.Flags().Int("nodes", 0, "Number of group instances")
	cmd.Flags().Int("cores", 0, "CPU cores per task")
	cmd.Flags().String("mem", "", "Memory per task (e.g. 8G)")
	cmd.Flags().Int("gpus", 0, "GPU count")
	cmd.Flags().String("time", "", "Walltime limit HH:MM:SS")
	cmd.Flags().String("chdir", "", "Working directory")
	cmd.Flags().String("depend", "", "Dependency spec (complete:<job-id>)")
	cmd.Flags().String("driver", "", "Task driver")
	cmd.Flags().String("reschedule-mode", "", "Job reschedule mode (e.g. delay, fail)")
	cmd.Flags().Int("reschedule-attempts", 0, "Job max reschedule attempts")
	cmd.Flags().String("reschedule-interval", "", "Reschedule interval (e.g. 30s)")
	cmd.Flags().String("reschedule-delay", "", "Reschedule delay (e.g. 5s)")
	cmd.Flags().String("reschedule-max-delay", "", "Reschedule max delay (e.g. 1m)")
	cmd.Flags().String("output", "", "Logical stdout file path in metadata")
	cmd.Flags().String("error", "", "Logical stderr file path in metadata")
	cmd.Flags().StringToString("meta", nil, "Job meta key=value")
	cmd.Flags().StringSlice("port", nil, "Named network ports")
	cmd.Flags().String("params-file", "", "Param file path (YAML).")
	cmd.Flags().Bool("no-network", false, "Disable network access for this job")
	cmd.Flags().Bool("submit", false, "Submit job to Nomad instead of printing HCL")
	cmd.Flags().Bool("dry-run", false, "Plan job server-side without submitting")
	cmd.Flags().Bool("watch", false, "Stream logs after --submit")
	cmd.Flags().String("output-file", "", "Write generated HCL to file instead of stdout")
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
		for k, val := range m {
			spec.Meta[k] = val
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

	// env overrides script preamble
	envSpec := readNomadEnvVars()
	spec := mergeSpec(scriptSpec, envSpec)

	paramsFile, _ := cmd.Flags().GetString("params-file")
	if paramsFile != "" {
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

	if spec.Meta == nil {
		spec.Meta = map[string]string{}
	}
	spec.Meta["abc_submission_id"] = newSubmissionID()

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

	watch, _ := cmd.Flags().GetBool("watch")
	if watch {
		fmt.Fprintln(cmd.ErrOrStderr(), "\n  Waiting for allocation...")
		return watchJobLogs(ctx, nc, spec.Name, spec.Namespace, out, watchDelay, watchTimeout)
	}

	fmt.Fprintf(out, "\n  Track progress:\n")
	fmt.Fprintf(out, "    abc job logs %s --follow\n", spec.Name)
	fmt.Fprintf(out, "    abc job show %s\n", spec.Name)
	return nil
}

func newSubmissionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("sub-%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}

func isHCLLiteral(val string) bool {
	if val == "" {
		return false
	}
	trim := strings.TrimSpace(val)
	if trim == "true" || trim == "false" {
		return true
	}
	if _, err := strconv.ParseFloat(trim, 64); err == nil {
		return true
	}
	if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
		return true
	}
	if strings.HasPrefix(trim, "{") && strings.HasSuffix(trim, "}") {
		return true
	}
	return false
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
	watchDelay = 10 * time.Second
	watchTimeout = 5 * time.Minute
)

func watchJobLogs(ctx context.Context, nc *nomadClient, jobID, namespace string, w io.Writer, delay, timeout time.Duration) error {
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

			if chosen.ClientStatus == "running" {
				return nc.StreamLogs(ctx, chosen.ID, task, "stdout", "start", 0, true, w)
			}
			return nc.StreamLogs(ctx, chosen.ID, task, "stdout", "start", 0, false, w)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-sleepCh(int(delay.Seconds())):
		}
	}
}

// ── Preamble parser ───────────────────────────────────────────────────────────

// stripInlineComment removes a trailing shell comment from a directive string.
// A comment begins at the first occurrence of " #" (space then hash).
// This allows annotated lines such as:
//
//	#ABC --cores=8    # 8 cores per task
func stripInlineComment(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func parsePreamble(r io.Reader) (abcDirs, nomadDirs []string, err error) {
	scanner := bufio.NewScanner(r)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if first {
			first = false
			if strings.HasPrefix(trimmed, "#!") {
				continue
			}
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		switch {
		case strings.HasPrefix(trimmed, "#ABC"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#ABC"))
			rest = stripInlineComment(rest)
			if rest != "" {
				abcDirs = append(abcDirs, rest)
			}
		case strings.HasPrefix(trimmed, "#NOMAD"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#NOMAD"))
			rest = stripInlineComment(rest)
			if rest != "" {
				nomadDirs = append(nomadDirs, rest)
			}
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, nil, fmt.Errorf("error reading script: %w", scanErr)
	}
	return abcDirs, nomadDirs, nil
}

func readNomadEnvVars() *jobSpec {
	spec := &jobSpec{}
	spec.Name = os.Getenv("NOMAD_JOB_NAME")
	spec.Namespace = os.Getenv("NOMAD_NAMESPACE")
	if v := os.Getenv("NOMAD_GROUP_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			spec.Nodes = n
		}
	}
	if v := os.Getenv("NOMAD_CPU_CORES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			spec.Cores = n
		}
	}
	if v := os.Getenv("NOMAD_MEMORY_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			spec.MemoryMB = n
		}
	}
	return spec
}

func mergeSpec(base, override *jobSpec) *jobSpec {
	if base == nil {
		base = &jobSpec{}
	}
	if override == nil {
		return base
	}
	if override.Name != "" {
		base.Name = override.Name
	}
	if override.Namespace != "" {
		base.Namespace = override.Namespace
	}
	if override.Region != "" {
		base.Region = override.Region
	}
	if len(override.Datacenters) > 0 {
		base.Datacenters = append([]string(nil), override.Datacenters...)
	}
	if override.Priority != 0 {
		base.Priority = override.Priority
	}
	if override.Nodes != 0 {
		base.Nodes = override.Nodes
	}
	if override.Cores != 0 {
		base.Cores = override.Cores
	}
	if override.MemoryMB != 0 {
		base.MemoryMB = override.MemoryMB
	}
	if override.GPUs != 0 {
		base.GPUs = override.GPUs
	}
	if override.WalltimeSecs != 0 {
		base.WalltimeSecs = override.WalltimeSecs
	}
	if override.ChDir != "" {
		base.ChDir = override.ChDir
	}
	if override.Depend != "" {
		base.Depend = override.Depend
	}
	if override.Driver != "" {
		base.Driver = override.Driver
	}
	if override.DriverConfig != nil {
		if base.DriverConfig == nil {
			base.DriverConfig = map[string]string{}
		}
		for k, v := range override.DriverConfig {
			base.DriverConfig[k] = v
		}
	}
	if override.Meta != nil {
		if base.Meta == nil {
			base.Meta = map[string]string{}
		}
		for k, v := range override.Meta {
			base.Meta[k] = v
		}
	}
	if len(override.Ports) > 0 {
		base.Ports = append([]string(nil), override.Ports...)
	}
	// strategy: set bool flags if true (explicit true wins)
	if override.ExposeAllocID {
		base.ExposeAllocID = true
	}
	if override.ExposeShortAllocID {
		base.ExposeShortAllocID = true
	}
	if override.ExposeAllocName {
		base.ExposeAllocName = true
	}
	if override.ExposeAllocIndex {
		base.ExposeAllocIndex = true
	}
	if override.ExposeJobID {
		base.ExposeJobID = true
	}
	if override.ExposeJobName {
		base.ExposeJobName = true
	}
	if override.ExposeParentJobID {
		base.ExposeParentJobID = true
	}
	if override.ExposeGroupName {
		base.ExposeGroupName = true
	}
	if override.ExposeTaskName {
		base.ExposeTaskName = true
	}
	if override.ExposeNamespaceEnv {
		base.ExposeNamespaceEnv = true
	}
	if override.ExposeDCEnv {
		base.ExposeDCEnv = true
	}
	if override.ExposeCPULimit {
		base.ExposeCPULimit = true
	}
	if override.ExposeCPUCores {
		base.ExposeCPUCores = true
	}
	if override.ExposeMemLimit {
		base.ExposeMemLimit = true
	}
	if override.ExposeMemMaxLimit {
		base.ExposeMemMaxLimit = true
	}
	if override.ExposeAllocDir {
		base.ExposeAllocDir = true
	}
	if override.ExposeTaskDir {
		base.ExposeTaskDir = true
	}
	if override.ExposeSecretsDir {
		base.ExposeSecretsDir = true
	}
	return base
}

func flattenParams(prefix string, value any, out *[]string) error {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			newKey := k
			if prefix != "" {
				newKey = prefix + "." + k
			}
			if err := flattenParams(newKey, x, out); err != nil {
				return err
			}
		}
	case map[string]string:
		for k, x := range v {
			newKey := k
			if prefix != "" {
				newKey = prefix + "." + k
			}
			if err := flattenParams(newKey, x, out); err != nil {
				return err
			}
		}
	case []any:
		parts := make([]string, 0, len(v))
		for _, x := range v {
			parts = append(parts, fmt.Sprintf("%v", x))
		}
		*out = append(*out, fmt.Sprintf("--%s=[%s]", prefix, strings.Join(parts, ",")))
	case []string:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = fmt.Sprintf("\"%s\"", x)
		}
		*out = append(*out, fmt.Sprintf("--%s=[%s]", prefix, strings.Join(parts, ",")))
	case bool:
		if v {
			*out = append(*out, fmt.Sprintf("--%s", prefix))
		}
	case nil:
		// ignore
	default:
		val := fmt.Sprintf("%v", v)
		// quote strings containing whitespace or special chars
		if s, ok := v.(string); ok {
			if strings.ContainsAny(s, " \",:[]{}") {
				val = fmt.Sprintf("\"%s\"", s)
			}
		}
		*out = append(*out, fmt.Sprintf("--%s=%s", prefix, val))
	}
	return nil
}

func loadParamsFile(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := yaml.Unmarshal(bytes, &data); err != nil {
		return nil, fmt.Errorf("failed to parse params file as YAML: %w", err)
	}
	out := []string{}
	for k, v := range data {
		if err := flattenParams(k, v, &out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func resolveSpec(abcDirs, nomadDirs []string, defaultName string) (*jobSpec, error) {
	spec := &jobSpec{}
	for _, d := range nomadDirs {
		if err := applyDirective(spec, d, "NOMAD"); err != nil {
			return nil, err
		}
	}
	for _, d := range abcDirs {
		if err := applyDirective(spec, d, "ABC"); err != nil {
			return nil, err
		}
	}
	if spec.Name == "" {
		spec.Name = defaultName
	}
	if spec.Nodes == 0 {
		spec.Nodes = 1
	}
	if spec.Driver == "" {
		spec.Driver = "exec"
	}
	if spec.Priority == 0 {
		spec.Priority = 50
	}
	if spec.RescheduleMode != "" || spec.RescheduleAttempts != 0 || spec.RescheduleInterval != "" || spec.RescheduleDelay != "" || spec.RescheduleMaxDelay != "" {
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		spec.Meta["abc_reschedule_mode"] = spec.RescheduleMode
		if spec.RescheduleAttempts != 0 {
			spec.Meta["abc_reschedule_attempts"] = fmt.Sprintf("%d", spec.RescheduleAttempts)
		}
		if spec.RescheduleInterval != "" {
			spec.Meta["abc_reschedule_interval"] = spec.RescheduleInterval
		}
		if spec.RescheduleDelay != "" {
			spec.Meta["abc_reschedule_delay"] = spec.RescheduleDelay
		}
		if spec.RescheduleMaxDelay != "" {
			spec.Meta["abc_reschedule_max_delay"] = spec.RescheduleMaxDelay
		}
	}
	if spec.OutputLog != "" || spec.ErrorLog != "" {
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		if spec.OutputLog != "" {
			spec.Meta["abc_output"] = spec.OutputLog
		}
		if spec.ErrorLog != "" {
			spec.Meta["abc_error"] = spec.ErrorLog
		}
	}
	if spec.NoNetwork && len(spec.Ports) > 0 {
		return nil, fmt.Errorf("no-network cannot be combined with port mapping")
	}
	if spec.Name == "" {
		return nil, fmt.Errorf("job name is required: set #ABC --name=<n>, #NOMAD --name=<n>, or NOMAD_JOB_NAME")
	}
	return spec, nil
}

func applyDirective(spec *jobSpec, directive, marker string) error {
	for _, field := range strings.Fields(directive) {
		if !strings.HasPrefix(field, "--") {
			return fmt.Errorf("invalid #%s directive %q: expected --key or --key=value", marker, field)
		}
		kv := strings.SplitN(strings.TrimPrefix(field, "--"), "=", 2)
		key := kv[0]
		hasValue := len(kv) == 2
		val := ""
		if hasValue {
			val = strings.TrimSpace(kv[1])
			val = strings.Trim(val, "'\"")
		}

		if strings.HasPrefix(key, "driver.config.") {
			if !hasValue || strings.TrimSpace(val) == "" {
				return fmt.Errorf("#%s --%s requires a value", marker, key)
			}
			if spec.DriverConfig == nil {
				spec.DriverConfig = make(map[string]string)
			}
			spec.DriverConfig[strings.TrimPrefix(key, "driver.config.")] = val
			continue
		}

		switch key {
		// ── Scheduler directives ─────────────────────────────────────────────
		case "name":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --name requires a value", marker)
			}
			spec.Name = val
		case "region":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --region requires a value", marker)
			}
			spec.Region = val
		case "namespace":
			if !hasValue {
				spec.ExposeNamespaceEnv = true
			} else {
				spec.Namespace = val
			}
		case "dc":
			if !hasValue {
				spec.ExposeDCEnv = true
			} else {
				spec.Datacenters = append(spec.Datacenters, val)
			}
		case "priority":
			if !hasValue {
				return fmt.Errorf("#%s --priority requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--priority must be a positive integer, got %q", val)
			}
			spec.Priority = n
		case "nodes":
			if !hasValue {
				return fmt.Errorf("#%s --nodes requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--nodes must be a positive integer, got %q", val)
			}
			spec.Nodes = n
		case "cores":
			if !hasValue {
				return fmt.Errorf("#%s --cores requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--cores must be a positive integer, got %q", val)
			}
			spec.Cores = n
		case "mem":
			if !hasValue {
				return fmt.Errorf("#%s --mem requires a value", marker)
			}
			mb, err := parseMemoryMB(val)
			if err != nil {
				return err
			}
			spec.MemoryMB = mb
		case "gpus":
			if !hasValue {
				return fmt.Errorf("#%s --gpus requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("--gpus must be a positive integer, got %q", val)
			}
			spec.GPUs = n
		case "time":
			if !hasValue {
				return fmt.Errorf("#%s --time requires a value", marker)
			}
			secs, err := walltimeToSeconds(val)
			if err != nil {
				return err
			}
			spec.WalltimeSecs = secs
		case "chdir":
			if !hasValue {
				return fmt.Errorf("#%s --chdir requires a value", marker)
			}
			spec.ChDir = val
		case "depend":
			if !hasValue {
				return fmt.Errorf("#%s --depend requires a value", marker)
			}
			spec.Depend = val
		case "driver":
			if !hasValue {
				return fmt.Errorf("#%s --driver requires a value", marker)
			}
			spec.Driver = val
		case "reschedule-mode":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-mode requires a value", marker)
			}
			spec.RescheduleMode = val
		case "reschedule-attempts":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-attempts requires a value", marker)
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return fmt.Errorf("--reschedule-attempts must be non-negative, got %q", val)
			}
			spec.RescheduleAttempts = n
		case "reschedule-interval":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-interval requires a value", marker)
			}
			spec.RescheduleInterval = val
		case "reschedule-delay":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-delay requires a value", marker)
			}
			spec.RescheduleDelay = val
		case "reschedule-max-delay":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --reschedule-max-delay requires a value", marker)
			}
			spec.RescheduleMaxDelay = val
		case "output":
			if !hasValue {
				return fmt.Errorf("#%s --output requires a value", marker)
			}
			spec.OutputLog = val
		case "error":
			if !hasValue {
				return fmt.Errorf("#%s --error requires a value", marker)
			}
			spec.ErrorLog = val
		case "constraint":
			if !hasValue {
				return fmt.Errorf("#%s --constraint requires a value", marker)
			}
			c, err := parseConstraint(val)
			if err != nil {
				return err
			}
			spec.Constraints = append(spec.Constraints, c)
		case "affinity":
			if !hasValue {
				return fmt.Errorf("#%s --affinity requires a value", marker)
			}
			a, err := parseAffinity(val)
			if err != nil {
				return err
			}
			spec.Affinities = append(spec.Affinities, a)

		// ── Meta directive ───────────────────────────────────────────────────
		case "meta":
			if !hasValue {
				return fmt.Errorf("#%s --meta requires key=value format", marker)
			}
			parts := strings.SplitN(val, "=", 2)
			if len(parts) != 2 || parts[0] == "" {
				return fmt.Errorf("#%s --meta requires key=value format, got %q", marker, val)
			}
			if spec.Meta == nil {
				spec.Meta = make(map[string]string)
			}
			spec.Meta[parts[0]] = parts[1]

		// ── Network directive ────────────────────────────────────────────────
		case "port":
			if !hasValue || val == "" {
				return fmt.Errorf("#%s --port requires a label value", marker)
			}
			if spec.NoNetwork {
				return fmt.Errorf("#%s --port cannot be used with --no-network", marker)
			}
			spec.Ports = append(spec.Ports, val)
		case "no-network":
			if hasValue {
				return fmt.Errorf("#%s --no-network does not accept a value", marker)
			}
			spec.NoNetwork = true

		// ── Runtime-exposure boolean flags ───────────────────────────────────
		case "alloc_id":
			spec.ExposeAllocID = true
		case "short_alloc_id":
			spec.ExposeShortAllocID = true
		case "alloc_name":
			spec.ExposeAllocName = true
		case "alloc_index":
			spec.ExposeAllocIndex = true
		case "job_id":
			spec.ExposeJobID = true
		case "job_name":
			spec.ExposeJobName = true
		case "parent_job_id":
			spec.ExposeParentJobID = true
		case "group_name":
			spec.ExposeGroupName = true
		case "task_name":
			spec.ExposeTaskName = true
		case "cpu_limit":
			spec.ExposeCPULimit = true
		case "cpu_cores":
			spec.ExposeCPUCores = true
		case "mem_limit":
			spec.ExposeMemLimit = true
		case "mem_max_limit":
			spec.ExposeMemMaxLimit = true
		case "alloc_dir":
			spec.ExposeAllocDir = true
		case "task_dir":
			spec.ExposeTaskDir = true
		case "secrets_dir":
			spec.ExposeSecretsDir = true

		default:
			return fmt.Errorf("unknown #%s directive --%s", marker, key)
		}
	}
	return nil
}

func parseConstraint(expr string) (nomadConstraint, error) {
	expr = strings.TrimSpace(expr)
	ops := []string{"==", "!=", "=~", "!~", "<", "<=", ">", ">="}
	for _, op := range ops {
		if idx := strings.Index(expr, op); idx >= 0 {
			attr := strings.TrimSpace(expr[:idx])
			val := strings.TrimSpace(expr[idx+len(op):])
			if attr == "" || val == "" {
				return nomadConstraint{}, fmt.Errorf("invalid constraint expression %q", expr)
			}
			val = strings.Trim(val, "'\"")
			return nomadConstraint{Attribute: attr, Operator: op, Value: val}, nil
		}
	}
	return nomadConstraint{}, fmt.Errorf("invalid constraint expression %q", expr)
}

func parseAffinity(specExpr string) (nomadAffinity, error) {
	specExpr = strings.TrimSpace(specExpr)
	weight := 50
	parts := strings.Split(specExpr, ",")
	if len(parts) == 0 {
		return nomadAffinity{}, fmt.Errorf("invalid affinity expression %q", specExpr)
	}
	main := strings.TrimSpace(parts[0])
	if main == "" {
		return nomadAffinity{}, fmt.Errorf("invalid affinity expression %q", specExpr)
	}
	c, err := parseConstraint(main)
	if err != nil {
		return nomadAffinity{}, err
	}
	for _, p := range parts[1:] {
		if strings.HasPrefix(strings.TrimSpace(p), "weight=") {
			wStr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "weight="))
			w, err := strconv.Atoi(wStr)
			if err != nil || w < 0 {
				return nomadAffinity{}, fmt.Errorf("invalid affinity weight %q", wStr)
			}
			weight = w
		}
	}
	return nomadAffinity{Attribute: c.Attribute, Operator: c.Operator, Value: c.Value, Weight: weight}, nil
}

// ── HCL generator ─────────────────────────────────────────────────────────────

func generateHCL(spec *jobSpec, scriptName, scriptContent string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "job %q {\n", spec.Name)
	fmt.Fprintf(&b, "  type     = \"batch\"\n")
	fmt.Fprintf(&b, "  priority = %d\n", spec.Priority)
	if spec.Region != "" {
		fmt.Fprintf(&b, "  region   = %q\n", spec.Region)
	}
	if spec.Namespace != "" {
		fmt.Fprintf(&b, "  namespace = %q\n", spec.Namespace)
	}
	if len(spec.Datacenters) > 0 {
		dcs := make([]string, len(spec.Datacenters))
		for i, dc := range spec.Datacenters {
			dcs[i] = fmt.Sprintf("%q", dc)
		}
		fmt.Fprintf(&b, "  datacenters = [%s]\n", strings.Join(dcs, ", "))
	}
	for _, c := range spec.Constraints {
		fmt.Fprintf(&b, "  constraint {\n")
		fmt.Fprintf(&b, "    attribute = %q\n", c.Attribute)
		fmt.Fprintf(&b, "    operator  = %q\n", c.Operator)
		fmt.Fprintf(&b, "    value     = %q\n", c.Value)
		fmt.Fprintf(&b, "  }\n")
	}
	for _, a := range spec.Affinities {
		fmt.Fprintf(&b, "  affinity {\n")
		fmt.Fprintf(&b, "    attribute = %q\n", a.Attribute)
		fmt.Fprintf(&b, "    operator  = %q\n", a.Operator)
		fmt.Fprintf(&b, "    value     = %q\n", a.Value)
		fmt.Fprintf(&b, "    weight    = %d\n", a.Weight)
		fmt.Fprintf(&b, "  }\n")
	}
	fmt.Fprintln(&b)

	if len(spec.Meta) > 0 {
		fmt.Fprintf(&b, "  meta {\n")
		for _, k := range sortedKeys(spec.Meta) {
			fmt.Fprintf(&b, "    %s = %q\n", k, spec.Meta[k])
		}
		fmt.Fprintf(&b, "  }\n\n")
	}

	fmt.Fprintf(&b, "  group \"main\" {\n")
	fmt.Fprintf(&b, "    count = %d\n", spec.Nodes)

	if spec.NoNetwork {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "    network {\n")
		fmt.Fprintf(&b, "      mode = \"none\"\n")
		fmt.Fprintf(&b, "    }\n")
	} else if len(spec.Ports) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "    network {\n")
		for _, p := range spec.Ports {
			fmt.Fprintf(&b, "      port %q {}\n", p)
		}
		fmt.Fprintf(&b, "    }\n")
	}

	if spec.Depend != "" {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "    task \"wait-dependency\" {\n")
		fmt.Fprintf(&b, "      driver = %q\n", spec.Driver)
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "      lifecycle {\n")
		fmt.Fprintf(&b, "        hook    = \"prestart\"\n")
		fmt.Fprintf(&b, "        sidecar = false\n")
		fmt.Fprintf(&b, "      }\n\n")
		fmt.Fprintf(&b, "      config {\n")
		fmt.Fprintf(&b, "        command = \"/bin/sh\"\n")
		fmt.Fprintf(&b, "        args    = [\"-c\", \"echo Waiting for dependency: %s\"]\n", spec.Depend)
		fmt.Fprintf(&b, "      }\n")
		fmt.Fprintf(&b, "    }\n")
	}

	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "    task \"main\" {\n")
	fmt.Fprintf(&b, "      driver = %q\n", spec.Driver)
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "      config {\n")
	var cmdExpr string
	if spec.WalltimeSecs > 0 {
		cmdExpr = fmt.Sprintf("timeout %d /bin/bash local/%s", spec.WalltimeSecs, scriptName)
	} else {
		cmdExpr = fmt.Sprintf("/bin/bash local/%s", scriptName)
	}

	if spec.OutputLog != "" || spec.ErrorLog != "" {
		// Preserve default stdout/stderr (Nomad log capture), while also writing copies.
		redirectCmd := cmdExpr
		if spec.OutputLog != "" {
			redirectCmd = fmt.Sprintf("%s 1> >(tee -a \"${NOMAD_TASK_DIR}/%s\")", redirectCmd, spec.OutputLog)
		}
		if spec.ErrorLog != "" {
			redirectCmd = fmt.Sprintf("%s 2> >(tee -a \"${NOMAD_TASK_DIR}/%s\" >&2)", redirectCmd, spec.ErrorLog)
		}
		fmt.Fprintf(&b, "        command  = \"/bin/bash\"\n")
		fmt.Fprintf(&b, "        args     = [\"-lc\", %q]\n", redirectCmd)
	} else {
		if spec.WalltimeSecs > 0 {
			fmt.Fprintf(&b, "        command  = \"timeout\"\n")
			fmt.Fprintf(&b, "        args     = [\"%d\", \"/bin/bash\", \"local/%s\"]\n", spec.WalltimeSecs, scriptName)
		} else {
			fmt.Fprintf(&b, "        command  = \"/bin/bash\"\n")
			fmt.Fprintf(&b, "        args     = [\"local/%s\"]\n", scriptName)
		}
	}
	if spec.ChDir != "" {
		fmt.Fprintf(&b, "        work_dir = %q\n", spec.ChDir)
	}
	for _, k := range sortedKeys(spec.DriverConfig) {
		v := strings.TrimSpace(spec.DriverConfig[k])
		if isHCLLiteral(v) {
			fmt.Fprintf(&b, "        %s = %s\n", k, v)
		} else {
			fmt.Fprintf(&b, "        %s = %q\n", k, v)
		}
	}
	fmt.Fprintf(&b, "      }\n\n")

	delimiter := heredocDelimiter(scriptContent)
	fmt.Fprintf(&b, "      template {\n")
	fmt.Fprintf(&b, "        data = <<-%s\n", delimiter)
	fmt.Fprint(&b, scriptContent)
	if !strings.HasSuffix(scriptContent, "\n") {
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "%s\n", delimiter)
	fmt.Fprintf(&b, "        destination = %q\n", filepath.ToSlash(filepath.Join("local", scriptName)))
	fmt.Fprintf(&b, "        perms       = \"0755\"\n")
	fmt.Fprintf(&b, "      }\n")

	if spec.Cores > 0 || spec.MemoryMB > 0 || spec.GPUs > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "      resources {\n")
		if spec.Cores > 0 {
			fmt.Fprintf(&b, "        cores  = %d\n", spec.Cores)
		}
		if spec.MemoryMB > 0 {
			fmt.Fprintf(&b, "        memory = %d\n", spec.MemoryMB)
		}
		if spec.GPUs > 0 {
			fmt.Fprintln(&b)
			fmt.Fprintf(&b, "        device \"nvidia/gpu\" {\n")
			fmt.Fprintf(&b, "          count = %d\n", spec.GPUs)
			fmt.Fprintf(&b, "        }\n")
		}
		fmt.Fprintf(&b, "      }\n")
	}

	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "      env {\n")

	// HPC compatibility layer — always emitted.
	fmt.Fprintf(&b, "        # HPC compat: PBS/SLURM aliases\n")
	fmt.Fprintf(&b, "        SLURM_JOB_ID        = \"${NOMAD_ALLOC_ID}\"\n")
	fmt.Fprintf(&b, "        PBS_JOBID           = \"${NOMAD_ALLOC_ID}\"\n")
	fmt.Fprintf(&b, "        SLURM_JOB_NAME      = \"${NOMAD_JOB_NAME}\"\n")
	fmt.Fprintf(&b, "        PBS_JOBNAME         = \"${NOMAD_JOB_NAME}\"\n")
	fmt.Fprintf(&b, "        SLURM_SUBMIT_DIR    = \"${NOMAD_TASK_DIR}\"\n")
	fmt.Fprintf(&b, "        PBS_O_WORKDIR       = \"${NOMAD_TASK_DIR}\"\n")
	fmt.Fprintf(&b, "        SLURM_ARRAY_TASK_ID = \"${NOMAD_ALLOC_INDEX}\"\n")
	fmt.Fprintf(&b, "        PBS_ARRAYID         = \"${NOMAD_ALLOC_INDEX}\"\n")
	fmt.Fprintf(&b, "        SLURM_NTASKS        = \"${NOMAD_GROUP_COUNT}\"\n")
	fmt.Fprintf(&b, "        PBS_NP              = \"${NOMAD_GROUP_COUNT}\"\n")
	fmt.Fprintf(&b, "        SLURMD_NODENAME     = \"${NOMAD_ALLOC_HOST}\"\n")
	fmt.Fprintf(&b, "        PBS_O_HOST          = \"${NOMAD_ALLOC_HOST}\"\n")
	fmt.Fprintf(&b, "        SLURM_CPUS_ON_NODE  = \"${NOMAD_CPU_CORES}\"\n")
	fmt.Fprintf(&b, "        PBS_NUM_PPN         = \"${NOMAD_CPU_CORES}\"\n")
	fmt.Fprintf(&b, "        SLURM_MEM_PER_NODE  = \"${NOMAD_MEMORY_LIMIT}\"\n")
	fmt.Fprintf(&b, "        PBS_MEM             = \"${NOMAD_MEMORY_LIMIT}\"\n")

	type runtimeVar struct {
		flag bool
		env  string
	}
	exposures := []runtimeVar{
		{spec.ExposeAllocID, "NOMAD_ALLOC_ID"},
		{spec.ExposeShortAllocID, "NOMAD_SHORT_ALLOC_ID"},
		{spec.ExposeAllocName, "NOMAD_ALLOC_NAME"},
		{spec.ExposeAllocIndex, "NOMAD_ALLOC_INDEX"},
		{spec.ExposeJobID, "NOMAD_JOB_ID"},
		{spec.ExposeJobName, "NOMAD_JOB_NAME"},
		{spec.ExposeParentJobID, "NOMAD_JOB_PARENT_ID"},
		{spec.ExposeGroupName, "NOMAD_GROUP_NAME"},
		{spec.ExposeTaskName, "NOMAD_TASK_NAME"},
		{spec.ExposeNamespaceEnv, "NOMAD_NAMESPACE"},
		{spec.ExposeDCEnv, "NOMAD_DC"},
		{spec.ExposeCPULimit, "NOMAD_CPU_LIMIT"},
		{spec.ExposeCPUCores, "NOMAD_CPU_CORES"},
		{spec.ExposeMemLimit, "NOMAD_MEMORY_LIMIT"},
		{spec.ExposeMemMaxLimit, "NOMAD_MEMORY_MAX_LIMIT"},
		{spec.ExposeAllocDir, "NOMAD_ALLOC_DIR"},
		{spec.ExposeTaskDir, "NOMAD_TASK_DIR"},
		{spec.ExposeSecretsDir, "NOMAD_SECRETS_DIR"},
	}
	hasExposures := false
	for _, e := range exposures {
		if e.flag {
			hasExposures = true
			break
		}
	}
	if hasExposures {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "        # Explicitly requested runtime variables\n")
		for _, e := range exposures {
			if e.flag {
				fmt.Fprintf(&b, "        %-28s = \"${%s}\"\n", e.env, e.env)
			}
		}
	}

	if len(spec.Ports) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "        # Network port variables\n")
		for _, p := range spec.Ports {
			up := strings.ToUpper(p)
			fmt.Fprintf(&b, "        NOMAD_IP_%-19s = \"${NOMAD_IP_%s}\"\n", up, p)
			fmt.Fprintf(&b, "        NOMAD_PORT_%-17s = \"${NOMAD_PORT_%s}\"\n", up, p)
			fmt.Fprintf(&b, "        NOMAD_ADDR_%-17s = \"${NOMAD_ADDR_%s}\"\n", up, p)
		}
	}

	fmt.Fprintf(&b, "      }\n")
	fmt.Fprintf(&b, "    }\n")
	fmt.Fprintf(&b, "  }\n")
	fmt.Fprintf(&b, "}\n")
	return b.String()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func parseMemoryMB(s string) (int, error) {
	upper := strings.ToUpper(strings.TrimSpace(s))
	if upper == "" {
		return 0, fmt.Errorf("empty memory value")
	}
	switch {
	case strings.HasSuffix(upper, "G"):
		n, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return n * 1024, nil
	case strings.HasSuffix(upper, "M"):
		n, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return n, nil
	case strings.HasSuffix(upper, "K"):
		n, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return (n + 1023) / 1024, nil
	default:
		n, err := strconv.Atoi(upper)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return n, nil
	}
}

func walltimeToSeconds(t string) (int, error) {
	parts := strings.Split(t, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid --time value %q: expected HH:MM:SS", t)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid --time value %q: %w", t, err)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid --time value %q: %w", t, err)
	}
	s, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, fmt.Errorf("invalid --time value %q: %w", t, err)
	}
	return h*3600 + m*60 + s, nil
}

func heredocDelimiter(scriptContent string) string {
	base := "ABC_SCRIPT"
	delimiter := base
	for i := 1; strings.Contains(scriptContent, delimiter); i++ {
		delimiter = fmt.Sprintf("%s_%d", base, i)
	}
	return delimiter
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
