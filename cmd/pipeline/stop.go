package pipeline

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// runTagPattern matches the prefix shared by a run's head job and every worker
// job the executor submits for it:
//
//	<user>-<runid>-nf-head-<pipeline>     the head
//	<user>-<runid>-<taskhash>-<PROCESS>   one worker per task
//
// The run tag is that leading `<user>-<runid>`, which is what makes the whole
// set addressable in one operation.
var runTagPattern = regexp.MustCompile(`^([A-Za-z0-9]+-\d+)(?:-.*)?$`)

// runTagOf extracts the run tag from a head job id, a worker job id, or a bare
// run tag. Returns "" when the argument does not carry one.
func runTagOf(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Tolerate a Nomad UI form of <job>@<namespace>.
	if i := strings.Index(s, "@"); i > 0 {
		s = s[:i]
	}
	m := runTagPattern.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <run>",
		Short: "Stop a pipeline run and every worker job it submitted",
		Long: `Stop a pipeline run and every worker job it submitted.

Stopping the head alone does not stop a run. The head has already submitted
its worker jobs to Nomad, and Nomad keeps running them — and keeps starting
queued ones as capacity frees — long after the head is gone. Draining those by
hand is what this command replaces.

<run> accepts the head job id, any worker job id, or the bare run tag; all
three carry the same <user>-<runid> prefix:

  abc pipeline stop abhinav-1788628378
  abc pipeline stop abhinav-1788628378-nf-head-my-pipeline
  abc pipeline stop abhinav-1788628378-eecd7d55-SELECT_CONCORDANT

The head is stopped first, so it cannot submit more work while the workers are
being stopped.`,
		Args: cobra.ExactArgs(1),
		RunE: runPipelineStop,
	}
	cmd.Flags().Bool("purge", false,
		"Also remove the job definitions from Nomad (they are otherwise kept for post-hoc inspection)")
	cmd.Flags().Bool("dry-run", false, "Show what would be stopped and exit")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	return cmd
}

func runPipelineStop(cmd *cobra.Command, args []string) error {
	tag := runTagOf(args[0])
	if tag == "" {
		return fmt.Errorf("could not read a run tag from %q — expected a head job id, a worker job id, "+
			"or a bare <user>-<runid> tag", args[0])
	}
	purge, _ := cmd.Flags().GetBool("purge")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")
	ns := namespaceFromCmd(cmd)
	out := cmd.OutOrStdout()

	nc := nomadClientFromCmd(cmd)
	jobs, err := nc.ListJobs(cmd.Context(), tag, ns)
	if err != nil {
		return fmt.Errorf("listing jobs for run %s: %w", tag, err)
	}

	// Partition into head and workers. Only jobs that are still doing something
	// need stopping; a job Nomad already considers dead is left alone so its
	// record stays queryable.
	//
	// The name prefix narrows the candidates server-side, but it does not decide
	// membership: nf-nomad stamps the correlation onto each worker's job meta
	// (nf_head_job_id / nf_session_name / abc_run_name), and that is what is
	// actually authoritative. Confirming against it rejects a neighbouring run
	// whose id merely shares a prefix, and identifies the head by what it says
	// it is rather than by the "-nf-head-" substring in its name.
	//
	// The job LIST stub carries no Meta, so this costs one fetch per candidate.
	// That is bounded by the size of one run.
	var head []string
	var workers []string
	for _, j := range jobs {
		if strings.EqualFold(j.Status, "dead") {
			continue
		}
		if runTagOf(j.ID) != tag {
			continue // prefix match from a different run, e.g. a longer runid
		}
		isHead := strings.Contains(j.ID, "-nf-head-")
		if d, err := nc.GetJob(cmd.Context(), j.ID, ns); err == nil && d != nil && len(d.Meta) > 0 {
			if !metaBelongsToRun(d.Meta, tag) {
				continue
			}
			if hj := strings.TrimSpace(d.Meta["nf_head_job_id"]); hj != "" {
				isHead = hj == j.ID
			}
		}
		if isHead {
			head = append(head, j.ID)
		} else {
			workers = append(workers, j.ID)
		}
	}
	sort.Strings(head)
	sort.Strings(workers)

	if len(head)+len(workers) == 0 {
		fmt.Fprintf(out, "  Nothing running for run %s.\n", tag)
		return nil
	}

	fmt.Fprintf(out, "  Run %s — %d head, %d worker job(s) still active\n", tag, len(head), len(workers))
	if dryRun {
		for _, id := range append(append([]string{}, head...), workers...) {
			fmt.Fprintf(out, "    would stop  %s\n", id)
		}
		return nil
	}

	if !yes {
		note := ""
		if purge {
			note = " and purge their definitions"
		}
		fmt.Fprintf(out, "  Stop %d job(s)%s? [y/N]: ", len(head)+len(workers), note)
		sc := bufio.NewScanner(os.Stdin)
		sc.Scan()
		a := strings.TrimSpace(strings.ToLower(sc.Text()))
		if a != "y" && a != "yes" {
			fmt.Fprintln(out, "  Aborted.")
			return nil
		}
	}

	// Head first: while it is alive it can submit more workers, so stopping it
	// last would race the drain.
	var failed int
	for _, id := range append(append([]string{}, head...), workers...) {
		if _, err := nc.StopJob(cmd.Context(), id, ns, purge); err != nil {
			failed++
			fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ %s: %v\n", id, err)
			continue
		}
	}

	stopped := len(head) + len(workers) - failed
	fmt.Fprintf(out, "  ✓ Stopped %d job(s)", stopped)
	if purge {
		fmt.Fprintf(out, " and purged their definitions")
	}
	fmt.Fprintln(out)
	if failed > 0 {
		return fmt.Errorf("%d job(s) could not be stopped", failed)
	}
	return nil
}

// metaBelongsToRun reports whether a job's Nomad meta identifies it as part of
// the given run. nf-nomad stamps these onto every worker it submits; any one of
// them is sufficient, and a job carrying none of them is left to the caller's
// name-based judgement.
func metaBelongsToRun(meta map[string]string, tag string) bool {
	if len(meta) == 0 {
		return true // nothing to contradict the name match
	}
	for _, k := range []string{"nf_session_name", "nf_head_job_id", "abc_run_name"} {
		v := strings.TrimSpace(meta[k])
		if v == "" {
			continue
		}
		if v == tag || runTagOf(v) == tag {
			return true
		}
	}
	// Meta is present and none of it names this run.
	return false
}
