package pipeline

// logs.go — `abc pipeline logs <run>` (Tier-1).
//
// Inspect a Nextflow pipeline run after the fact: a process-level overview
// (which tasks ran, their status), per-task drill-in (the literal
// .command.sh + stdout/stderr + the nf-nomad-s5cmd staging trace), and a
// bulk log-set pull. The canonical source is the S3 workdir that
// nf-nomad-s5cmd already populates; the process spine comes from the Nomad
// job list (child jobs `<run>-<hash8>-<PROCESS>`, retained ~1 year), so the
// overview works even after the run's allocations are GC'd.
//
// Tier-1 scope (this file):
//   - process spine + status from the Nomad job list (no cloudcache parse)
//   - --fields / --filter / --list-fields (the nextflow-log-style grammar)
//   - --task [--sample] [--command] → S3 .command.* + .nxf-debug.log
//   - --all --output → bulk pull of the per-task log set (no bulky data)
// Deferred (Tier-2, see brainstorms/abc-seedling-prod/2026-06-03-nextflow-log-feature-mapping.md):
//   cloudcache TaskEntry parse (precise submit/start/complete + requested
//   resources), and measured-resource fields (need the pipeline's trace{}).

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/credsource"
	"github.com/spf13/cobra"
)

// taskRec is one pipeline task, assembled from the Nomad job list (Tier-1).
type taskRec struct {
	Process string // e.g. "CALL_WF:GATK_HAPLOTYPE_CALLER" (": " restored from "_")
	Hash    string // hash8 — first 8 hex of the Nextflow task hash
	JobID   string // full Nomad job ID
	Status  string // display status: completed | failed | running | pending | dead
	Submit  int64  // Nomad SubmitTime (ns)
	Modify  int64  // Nomad ModifyTime (ns)
}

// logFields are the Tier-1 fields available from the Nomad spine. The
// resource/timing fields nextflow-log exposes need the cloudcache (Tier-2)
// or .command.trace (pipeline trace{}); listed by --list-fields with a note.
var logFields = []string{"process", "status", "hash", "job_id", "submit", "duration"}

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <run>",
		Short: "Inspect a pipeline run's tasks and logs (overview, per-task command/output)",
		Long: `Inspect a Nextflow pipeline run after it ran.

<run> is the run prefix (e.g. solar-civet-1780492799), the head job ID, or
a Nomad UI URL of the head job. The process spine comes from the Nomad job
list (child jobs are retained ~1 year), so this works even after the run's
allocations are GC'd. Per-task content is read from the S3 workdir that
nf-nomad-s5cmd populates.

Default (no --task): a process-level overview — which tasks ran and their
status. Use --fields / --filter to shape it (see --list-fields), same field
vocabulary as 'nextflow log'.

  abc pipeline logs <run>                          # overview table
  abc pipeline logs <run> -f process,status,hash   # custom columns
  abc pipeline logs <run> -F "status == failed"    # only failed tasks
  abc pipeline logs <run> --task GATK_HAPLOTYPE_CALLER          # drill into a process
  abc pipeline logs <run> --task LOFREQ_CALL --command         # just the .command.sh
  abc pipeline logs <run> --all --output ./logs/               # pull the log set

Resource/timing fields (peak_rss, %cpu, realtime, …) require the pipeline's
own trace{} config — abc does not enable it for you (lean config). Reports
(report.html, trace.txt) likewise come from the pipeline; abc retrieves
what's present.`,
		Args: cobra.ExactArgs(1),
		RunE: runPipelineLogs,
	}
	cmd.Flags().String("namespace", "", "Nomad namespace")
	cmd.Flags().StringP("task", "t", "", "Drill into tasks whose process matches this (case-insensitive substring)")
	cmd.Flags().String("sample", "", "With --task: narrow to a sample/tag (matched against staged filenames)")
	cmd.Flags().Bool("command", false, "With --task: print only the .command.sh")
	cmd.Flags().StringP("fields", "f", "", "Comma-separated overview columns (see --list-fields)")
	cmd.Flags().StringP("filter", "F", "", `Filter tasks: "field OP value" joined by &&/||; OP in == != =~`)
	cmd.Flags().BoolP("list-fields", "l", false, "List available fields and exit")
	cmd.Flags().Bool("all", false, "With --output: download the run's per-task log set from S3")
	cmd.Flags().String("output", "", "Directory to write logs into (with --all)")
	cmd.Flags().String("workdir", "", "Override the S3 workdir root (else derived from the head job)")
	cmd.Flags().Int("tail", 40, "With --task: trailing lines of .command.out/.err to show")
	return cmd
}

func runPipelineLogs(cmd *cobra.Command, args []string) error {
	if list, _ := cmd.Flags().GetBool("list-fields"); list {
		return printLogFields(cmd)
	}

	runPrefix := normalizeRunArg(args[0])
	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)

	stubs, err := nc.ListJobs(cmd.Context(), runPrefix, ns)
	if err != nil {
		return fmt.Errorf("listing Nomad jobs for run %q: %w", runPrefix, err)
	}
	if len(stubs) == 0 {
		return fmt.Errorf("no Nomad jobs found with prefix %q (namespace %q) — "+
			"check the run ID, or pass --namespace", runPrefix, ns)
	}

	var recs []taskRec
	for i := range stubs {
		proc, hash8, ok := parseChildJob(stubs[i].ID, runPrefix)
		if !ok {
			continue // head job, or a name that isn't a child task
		}
		recs = append(recs, taskRec{
			Process: proc,
			Hash:    hash8,
			JobID:   stubs[i].ID,
			Status:  displayPipelineStatus(stubs[i]),
			Submit:  stubs[i].SubmitTime,
			Modify:  stubs[i].ModifyTime,
		})
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Process != recs[j].Process {
			return recs[i].Process < recs[j].Process
		}
		return recs[i].Submit < recs[j].Submit
	})

	// Apply --filter.
	if expr, _ := cmd.Flags().GetString("filter"); strings.TrimSpace(expr) != "" {
		filtered := recs[:0]
		for _, r := range recs {
			ok, ferr := matchFilter(r, expr)
			if ferr != nil {
				return ferr
			}
			if ok {
				filtered = append(filtered, r)
			}
		}
		recs = filtered
	}

	// --task: drill into matching tasks (S3 content).
	if taskMatch, _ := cmd.Flags().GetString("task"); taskMatch != "" {
		return drillTasks(cmd, runPrefix, ns, nc, recs, taskMatch)
	}

	// --all --output: bulk pull.
	if all, _ := cmd.Flags().GetBool("all"); all {
		return pullAll(cmd, runPrefix, ns, nc, recs)
	}

	// Default: overview table.
	return printOverview(cmd, runPrefix, recs)
}

// ── run-arg normalisation ───────────────────────────────────────────────

// normalizeRunArg accepts a run prefix, a head job ID, or a Nomad UI URL of
// the head job, and returns the run prefix. The head job is
// "<run>-nf-head-<pipeline-slug>"; we strip from "-nf-head-" onward.
func normalizeRunArg(arg string) string {
	arg = strings.TrimSpace(arg)
	// Pull a job ID out of a UI URL if one was pasted.
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		if i := strings.Index(arg, "/ui/jobs/"); i >= 0 {
			rest := arg[i+len("/ui/jobs/"):]
			if j := strings.IndexAny(rest, "@/?"); j >= 0 {
				rest = rest[:j]
			}
			arg = rest
		}
	}
	if i := strings.Index(arg, "-nf-head-"); i >= 0 {
		return arg[:i]
	}
	return arg
}

// ── child-job parsing (pure, tested) ────────────────────────────────────

var hash8Re = regexp.MustCompile(`^[0-9a-f]{8}$`)

// parseChildJob decomposes a child job ID "<run>-<hash8>-<PROCESS>" into the
// process name and the 8-hex hash. Returns ok=false for the head job
// ("<run>-nf-head-…") or any ID that doesn't fit the child shape.
//
// Nextflow process names contain ':' (subworkflow scope) which nf-nomad
// renders as '_' in the job ID; we restore ':' for display by mapping the
// known "_WF_" / leading-scope underscores back is lossy, so we keep the
// raw underscore form but present it readably.
func parseChildJob(jobID, runPrefix string) (process, hash8 string, ok bool) {
	rest := strings.TrimPrefix(jobID, runPrefix+"-")
	if rest == jobID || rest == "" {
		return "", "", false // didn't carry the prefix
	}
	if strings.HasPrefix(rest, "nf-head-") || rest == "nf-head" {
		return "", "", false // the head job
	}
	i := strings.IndexByte(rest, '-')
	if i <= 0 {
		return "", "", false
	}
	hash8 = rest[:i]
	process = rest[i+1:]
	if !hash8Re.MatchString(hash8) || process == "" {
		return "", "", false
	}
	return process, hash8, true
}

// displayPipelineStatus maps a Nomad batch job stub to a researcher-facing
// status, distinguishing a clean completion from a failure (both report
// Nomad Status "dead").
func displayPipelineStatus(s utils.NomadJobStub) string {
	switch s.Status {
	case "running":
		return "running"
	case "pending":
		return "pending"
	case "dead":
		failed, complete := 0, 0
		for _, g := range s.JobSummary.Summary {
			failed += g.Failed
			complete += g.Complete
		}
		if failed > 0 {
			return "failed"
		}
		if complete > 0 {
			return "completed"
		}
		return "dead"
	default:
		return s.Status
	}
}

// ── filter (pure, tested) ───────────────────────────────────────────────

// matchFilter evaluates a simple boolean expression against a record.
// Grammar (a safe subset of nextflow log's -F, NOT Groovy eval):
//
//	<clause> ( (&& | ||) <clause> )*
//	<clause> := <field> <op> <value>     op ∈ { == != =~ }
//
// Fields: process, status, hash, job_id. Values are bare tokens (no quotes
// needed); =~ treats the value as a regexp. && binds tighter than ||.
func matchFilter(r taskRec, expr string) (bool, error) {
	// Split on || (lowest precedence), then && within each.
	for _, orClause := range splitTop(expr, "||") {
		all := true
		clauses := splitTop(orClause, "&&")
		if len(clauses) == 0 {
			continue
		}
		for _, c := range clauses {
			ok, err := evalClause(r, c)
			if err != nil {
				return false, err
			}
			if !ok {
				all = false
				break
			}
		}
		if all {
			return true, nil
		}
	}
	return false, nil
}

func splitTop(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func evalClause(r taskRec, clause string) (bool, error) {
	var op string
	for _, candidate := range []string{"=~", "!=", "=="} {
		if strings.Contains(clause, candidate) {
			op = candidate
			break
		}
	}
	if op == "" {
		return false, fmt.Errorf("filter clause %q: expected one of == != =~", clause)
	}
	i := strings.Index(clause, op)
	field := strings.TrimSpace(clause[:i])
	value := strings.TrimSpace(clause[i+len(op):])
	value = strings.Trim(value, `"'`)

	got, err := fieldValue(r, field)
	if err != nil {
		return false, err
	}
	switch op {
	case "==":
		return strings.EqualFold(got, value), nil
	case "!=":
		return !strings.EqualFold(got, value), nil
	case "=~":
		re, err := regexp.Compile("(?i)" + value)
		if err != nil {
			return false, fmt.Errorf("filter regexp %q: %w", value, err)
		}
		return re.MatchString(got), nil
	}
	return false, nil
}

func fieldValue(r taskRec, field string) (string, error) {
	switch strings.ToLower(field) {
	case "process":
		return r.Process, nil
	case "status":
		return r.Status, nil
	case "hash":
		return r.Hash, nil
	case "job_id", "jobid", "native_id":
		return r.JobID, nil
	default:
		return "", fmt.Errorf("unknown field %q (filterable: process, status, hash, job_id)", field)
	}
}

// ── overview render ─────────────────────────────────────────────────────

func printOverview(cmd *cobra.Command, runPrefix string, recs []taskRec) error {
	out := cmd.OutOrStdout()
	if len(recs) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "  No matching tasks for run %q.\n", runPrefix)
		return nil
	}

	fields := logFields
	if f, _ := cmd.Flags().GetString("fields"); strings.TrimSpace(f) != "" {
		fields = splitCSV(f)
	}

	// Header.
	fmt.Fprintf(cmd.ErrOrStderr(), "  Run %s — %d tasks\n\n", runPrefix, len(recs))
	rows := make([][]string, 0, len(recs)+1)
	rows = append(rows, upper(fields))
	var failed, completed int
	for _, r := range recs {
		switch r.Status {
		case "failed":
			failed++
		case "completed":
			completed++
		}
		rows = append(rows, rowFor(r, fields))
	}
	writeTable(out, rows)
	fmt.Fprintf(cmd.ErrOrStderr(), "\n  %d completed, %d failed, %d total\n", completed, failed, len(recs))
	return nil
}

func rowFor(r taskRec, fields []string) []string {
	cells := make([]string, len(fields))
	for i, f := range fields {
		switch strings.ToLower(f) {
		case "process":
			cells[i] = r.Process
		case "status":
			cells[i] = r.Status
		case "hash":
			cells[i] = r.Hash
		case "job_id", "jobid":
			cells[i] = r.JobID
		case "submit":
			cells[i] = fmtNanos(r.Submit)
		case "duration":
			cells[i] = fmtDurationNanos(r.Submit, r.Modify, r.Status)
		default:
			cells[i] = "—"
		}
	}
	return cells
}

// ── --task drill-in (S3 content) ────────────────────────────────────────

func drillTasks(cmd *cobra.Command, runPrefix, ns string, nc *utils.NomadClient, recs []taskRec, match string) error {
	out := cmd.OutOrStdout()
	var matched []taskRec
	lm := strings.ToLower(match)
	for _, r := range recs {
		if strings.Contains(strings.ToLower(r.Process), lm) {
			matched = append(matched, r)
		}
	}
	if len(matched) == 0 {
		return fmt.Errorf("no task whose process contains %q in run %q", match, runPrefix)
	}

	commandOnly, _ := cmd.Flags().GetBool("command")
	tail, _ := cmd.Flags().GetInt("tail")

	wd, wdErr := resolveWorkdirRoot(cmd, runPrefix, ns, nc)
	s3, s3Err := newS5(cmd)

	for _, r := range matched {
		if commandOnly {
			// Just the .command.sh, verbatim, for piping.
			if wdErr != nil || s3Err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"  [%s %s] cannot fetch .command.sh: %v\n", r.Process, r.Hash, firstErr(wdErr, s3Err))
				continue
			}
			body, err := fetchTaskFile(s3, wd, r.Hash, ".command.sh")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "  [%s %s] %v\n", r.Process, r.Hash, err)
				continue
			}
			fmt.Fprintln(out, strings.TrimRight(body, "\n"))
			continue
		}

		// Full drill-in header.
		fmt.Fprintf(out, "── %s  (%s)\n", r.Process, r.Status)
		fmt.Fprintf(out, "   nomad job : %s\n", r.JobID)
		fmt.Fprintf(out, "   live logs : abc job logs %s --task nf-task\n", r.JobID)
		fmt.Fprintf(out, "   job def   : abc job inspect %s\n", r.JobID)

		if wdErr != nil || s3Err != nil {
			fmt.Fprintf(out, "   (S3 content unavailable: %v)\n\n", firstErr(wdErr, s3Err))
			continue
		}
		taskDir, derr := resolveTaskDir(s3, wd, r.Hash)
		if derr != nil {
			fmt.Fprintf(out, "   (workdir not found on S3: %v)\n\n", derr)
			continue
		}
		fmt.Fprintf(out, "   workdir   : %s\n", taskDir)
		if ec, err := s3.cat(taskDir + ".exitcode"); err == nil {
			fmt.Fprintf(out, "   exitcode  : %s\n", strings.TrimSpace(ec))
		}
		printTaskSection(out, s3, taskDir, ".command.sh", 0)
		printTaskSection(out, s3, taskDir, ".command.err", tail)
		printTaskSection(out, s3, taskDir, ".command.out", tail)
		printTaskSection(out, s3, taskDir, ".nxf-debug.log", tail)
		fmt.Fprintln(out)
	}
	return nil
}

// printTaskSection cats one file under taskDir and prints it under a labelled
// banner. tail==0 → whole file; tail>0 → only the last `tail` lines. Missing
// or empty files are noted, not errored (a 0-byte .command.err is normal).
func printTaskSection(out io.Writer, s3 *s5, taskDir, name string, tail int) {
	body, err := s3.cat(taskDir + name)
	if err != nil {
		return // file absent for this task; skip silently
	}
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		fmt.Fprintf(out, "   ── %s ── (empty)\n", name)
		return
	}
	lines := strings.Split(body, "\n")
	trimmed := false
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
		trimmed = true
	}
	fmt.Fprintf(out, "   ── %s ──", name)
	if trimmed {
		fmt.Fprintf(out, " (last %d lines)", tail)
	}
	fmt.Fprintln(out)
	for _, l := range lines {
		fmt.Fprintf(out, "   %s\n", l)
	}
}

// ── bulk pull ───────────────────────────────────────────────────────────

func pullAll(cmd *cobra.Command, runPrefix, ns string, nc *utils.NomadClient, recs []taskRec) error {
	outDir, _ := cmd.Flags().GetString("output")
	if strings.TrimSpace(outDir) == "" {
		return fmt.Errorf("--all requires --output <dir>")
	}
	wd, err := resolveWorkdirRoot(cmd, runPrefix, ns, nc)
	if err != nil {
		return fmt.Errorf("resolve workdir root: %w (pass --workdir s3://…)", err)
	}
	s3, err := newS5(cmd)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	logFiles := []string{".command.sh", ".command.out", ".command.err", ".command.run", ".exitcode", ".nxf-debug.log"}
	w := cmd.OutOrStdout()
	pulled := 0
	for _, r := range recs {
		taskDir, derr := resolveTaskDir(s3, wd, r.Hash)
		if derr != nil {
			continue
		}
		dst := filepath.Join(outDir, sanitize(r.Process), r.Hash)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		for _, f := range logFiles {
			if body, err := s3.cat(taskDir + f); err == nil {
				_ = os.WriteFile(filepath.Join(dst, strings.TrimPrefix(f, ".")), []byte(body), 0o644)
			}
		}
		pulled++
	}
	fmt.Fprintf(w, "  pulled the log set for %d task(s) into %s\n", pulled, outDir)
	return nil
}

// ── workdir + task-dir resolution ───────────────────────────────────────

var workDirRe = regexp.MustCompile(`workDir\s*=\s*"([^"]+)"`)

// resolveWorkdirRoot returns the S3 workdir root for the run. Order:
//  1. --workdir override.
//  2. the head job's nextflow.headjob.config `workDir = "s3://…"` line
//     (Nomad retains the head job ~1 year), trimmed to the run prefix.
func resolveWorkdirRoot(cmd *cobra.Command, runPrefix, ns string, nc *utils.NomadClient) (string, error) {
	if w, _ := cmd.Flags().GetString("workdir"); strings.TrimSpace(w) != "" {
		return strings.TrimRight(w, "/") + "/", nil
	}
	stubs, err := nc.ListJobs(cmd.Context(), runPrefix, ns)
	if err != nil {
		return "", err
	}
	var headID string
	for i := range stubs {
		if strings.Contains(stubs[i].ID, "-nf-head-") {
			headID = stubs[i].ID
			break
		}
	}
	if headID == "" {
		return "", fmt.Errorf("head job not found for run %q", runPrefix)
	}
	job, err := nc.GetJob(cmd.Context(), headID, ns)
	if err != nil {
		return "", err
	}
	for _, tg := range job.TaskGroups {
		for _, t := range tg.Tasks {
			for _, tp := range t.Templates {
				if m := workDirRe.FindStringSubmatch(tp.EmbeddedTmpl); m != nil {
					return strings.TrimRight(m[1], "/") + "/", nil
				}
			}
		}
	}
	return "", fmt.Errorf("workDir not found in head job %q config", headID)
}

// resolveTaskDir maps a hash8 to the full S3 task dir under the workdir root:
// <workdir>/<hash8[:2]>/<dir starting with hash8[2:]>/.
func resolveTaskDir(s3 *s5, workdirRoot, hash8 string) (string, error) {
	if len(hash8) < 3 {
		return "", fmt.Errorf("hash too short: %q", hash8)
	}
	prefix := workdirRoot + hash8[:2] + "/"
	entries, err := s3.ls(prefix)
	if err != nil {
		return "", err
	}
	want := hash8[2:]
	if dir, ok := matchHashDir(entries, prefix, want); ok {
		return dir, nil
	}
	return "", fmt.Errorf("no task dir under %s starting with %q", prefix, want)
}

// matchHashDir finds the entry under prefix whose basename starts with want
// (the remaining hash chars after the 2-char shard). Pure for testing.
func matchHashDir(entries []string, prefix, want string) (string, bool) {
	for _, e := range entries {
		base := strings.TrimSuffix(strings.TrimPrefix(e, prefix), "/")
		if strings.HasPrefix(base, want) {
			return prefix + base + "/", true
		}
	}
	return "", false
}

// ── s5cmd helper (compact; reuses credsource Minio creds) ───────────────

type s5 struct {
	bin      string
	endpoint string
	env      []string
}

func newS5(cmd *cobra.Command) (*s5, error) {
	c, err := abccfg.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	ep, ak, sk := resolvePipelineS3Creds(cmd.Context(), c.ActiveCtx())
	if ep == "" || ak == "" || sk == "" {
		return nil, fmt.Errorf("no S3 endpoint/credentials in active context")
	}
	bin := ""
	if mp, mErr := utils.ManagedBinaryPath("s5cmd"); mErr == nil && mp != "" {
		if _, statErr := os.Stat(mp); statErr == nil {
			bin = mp // managed binary present on disk
		}
	}
	if bin == "" {
		if p, lookErr := exec.LookPath("s5cmd"); lookErr == nil {
			bin = p
		} else {
			return nil, fmt.Errorf("s5cmd not found on PATH or in ~/.abc/binaries (run `abc admin tools fetch s5cmd`)")
		}
	}
	return &s5{
		bin:      bin,
		endpoint: ep,
		env: append(os.Environ(),
			"AWS_ACCESS_KEY_ID="+ak,
			"AWS_SECRET_ACCESS_KEY="+sk,
		),
	}, nil
}

// resolvePipelineS3Creds returns (endpoint, accessKey, secretKey) for the
// run's bucket. Broker contexts (cred_source=seedling/v1) resolve via the
// credsource broker; everything else reads admin.services.{minio,rustfs}
// through GetAdminFloorField, which transparently handles both the flat
// `access_key`/`secret_key` form and the nested `cred_source.local` form
// used by admin/bootstrap contexts. Mirrors cmd/data's resolveS3Creds.
func resolvePipelineS3Creds(ctx context.Context, abcCtx abccfg.Context) (endpoint, accessKey, secretKey string) {
	if credsource.IsBroker(abcCtx.CredSource) {
		if creds, err := credsource.ResolveFromContext(ctx, abcCtx); err == nil {
			ep := strings.TrimRight(strings.TrimSpace(creds.Minio.Endpoint), "/")
			if ep != "" && creds.Minio.AccessKey != "" && creds.Minio.SecretKey != "" {
				return ep, creds.Minio.AccessKey, creds.Minio.SecretKey
			}
		}
	}
	for _, svc := range []string{"minio", "rustfs"} {
		ep, _ := abccfg.GetAdminFloorField(&abcCtx.Admin.Services, svc, "endpoint")
		ak, _ := abccfg.GetAdminFloorField(&abcCtx.Admin.Services, svc, "access_key")
		if ak == "" {
			ak, _ = abccfg.GetAdminFloorField(&abcCtx.Admin.Services, svc, "user")
		}
		sk, _ := abccfg.GetAdminFloorField(&abcCtx.Admin.Services, svc, "secret_key")
		if sk == "" {
			sk, _ = abccfg.GetAdminFloorField(&abcCtx.Admin.Services, svc, "password")
		}
		if ep != "" && ak != "" && sk != "" {
			return strings.TrimRight(ep, "/"), ak, sk
		}
	}
	return "", "", ""
}

func (s *s5) run(subArgs ...string) ([]byte, error) {
	args := append([]string{"--endpoint-url", s.endpoint}, subArgs...)
	c := exec.Command(s.bin, args...)
	c.Env = s.env
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

func (s *s5) cat(s3url string) (string, error) {
	b, err := s.run("cat", s3url)
	return string(b), err
}

// ls returns the full s3:// keys/prefixes directly under prefix.
func (s *s5) ls(prefix string) ([]string, error) {
	b, err := s.run("ls", prefix)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// s5cmd ls lines: "DIR  <name>/" or "<date> <time> <size> <name>".
		fields := strings.Fields(line)
		name := fields[len(fields)-1]
		out = append(out, prefix+name)
	}
	return out, nil
}

// fetchTaskFile resolves the task dir then cats one file under it.
func fetchTaskFile(s3 *s5, workdirRoot, hash8, name string) (string, error) {
	dir, err := resolveTaskDir(s3, workdirRoot, hash8)
	if err != nil {
		return "", err
	}
	return s3.cat(dir + name)
}

// ── small helpers ───────────────────────────────────────────────────────

func printLogFields(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Tier-1 fields (from the Nomad job list — always available):")
	for _, f := range logFields {
		fmt.Fprintf(out, "  %s\n", f)
	}
	fmt.Fprintln(out, "\nFilterable fields: process, status, hash, job_id")
	fmt.Fprintln(out, "\nNot in Tier-1 (need the pipeline's own trace{} config; abc does not enable it):")
	for _, f := range []string{"realtime", "%cpu", "%mem", "peak_rss", "peak_vmem", "rchar", "wchar"} {
		fmt.Fprintf(out, "  %s   (enable trace{} in your pipeline config)\n", f)
	}
	return nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func upper(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToUpper(s)
	}
	return out
}

func writeTable(w interface{ Write([]byte) (int, error) }, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, c := range row {
			if i < len(widths) && len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	for _, row := range rows {
		var b strings.Builder
		b.WriteString("  ")
		for i, c := range row {
			b.WriteString(c)
			if i < len(row)-1 {
				pad := widths[i] - len(c) + 2
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		b.WriteByte('\n')
		_, _ = w.Write([]byte(b.String()))
	}
}

func sanitize(s string) string {
	return strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(s)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// fmtNanos renders a Nomad ns-epoch timestamp as a compact local datetime.
func fmtNanos(ns int64) string {
	if ns <= 0 {
		return "—"
	}
	return time.Unix(0, ns).Format("2006-01-02 15:04")
}

// fmtDurationNanos renders submit→modify as a human duration for terminal
// tasks; running/pending tasks show "—" (no meaningful end yet).
func fmtDurationNanos(submit, modify int64, status string) string {
	if submit <= 0 || modify <= submit {
		return "—"
	}
	if status == "running" || status == "pending" {
		return "—"
	}
	d := time.Duration(modify - submit)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
