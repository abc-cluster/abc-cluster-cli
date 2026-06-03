package job

// inspect.go — `abc job inspect <job-id-or-url>`.
//
// Default: pretty-print the entrypoint shell script that the job runs —
// the thing a researcher actually cares about ("what did this job do?").
// The script is embedded in the Nomad job as a task `template { … }` stanza;
// we locate it by matching the script path in the task's `config.args`
// against each template's DestPath.
//
// With --def: print the full Nomad job definition (JSON, as the API
// returns it) instead. Useful for debugging placement, resources, env.
//
// Because the Nomad SERVER retains dead batch-job records for
// job_gc_threshold (1 year on seedling), `abc job inspect` keeps working
// long after the job's allocations are gone — you can recover exactly
// what ran without the job appearing in `abc job list` output.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <job-id-or-nomad-ui-url>",
		Short: "Show the shell script a job ran (or its full Nomad definition with --def)",
		Long: `Show what a job actually ran.

By default this pretty-prints the entrypoint shell script embedded in the
job — the part a researcher cares about. With --def, it prints the full
Nomad job definition (JSON) instead.

The positional accepts either a bare job ID or a full Nomad Web UI URL
(--namespace is auto-seeded from <job>@<ns> in the URL when present).

Because the Nomad server retains dead batch-job records for ~1 year,
'abc job inspect' works long after the job's allocations are GC'd — so
you can recover exactly what a completed job ran even when it no longer
appears in 'abc job list'.

Examples:
  abc job inspect my-job@su-demo            # pretty-print the shell script
  abc job inspect my-job --def              # full Nomad job definition (JSON)
  abc job inspect my-job --task nf-task     # disambiguate a multi-task group`,
		Args: cobra.ExactArgs(1),
		RunE: runInspect,
	}
	cmd.Flags().String("namespace", "", "Nomad namespace")
	cmd.Flags().Bool("def", false, "Print the full Nomad job definition (JSON) instead of the shell script")
	cmd.Flags().String("task", "", "Task name to inspect (default: the first task in the first group)")
	return cmd
}

func runInspect(cmd *cobra.Command, args []string) error {
	jobID, err := resolveJobArg(cmd, args[0])
	if err != nil {
		return err
	}
	ns := namespaceFromCmd(cmd)
	def, _ := cmd.Flags().GetBool("def")
	taskFilter, _ := cmd.Flags().GetString("task")

	nc := nomadClientFromCmd(cmd)
	job, err := nc.GetJob(cmd.Context(), jobID, ns)
	if err != nil {
		return fmt.Errorf("getting job %q: %w", jobID, err)
	}

	out := cmd.OutOrStdout()

	// --def: dump the full job definition as indented JSON.
	if def {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(job)
	}

	// Default: locate + print the entrypoint shell script.
	task, err := selectTask(job, taskFilter)
	if err != nil {
		return err
	}

	script, dest, ok := extractEntrypointScript(task)
	if !ok {
		// No embedded script — surface what the task does run so the user
		// isn't left guessing (e.g. a docker image with an inline command).
		fmt.Fprintf(cmd.ErrOrStderr(),
			"  No embedded shell script found for task %q (driver %q).\n"+
				"  This job may run a container image directly rather than an\n"+
				"  abc-generated script. Use --def to see the full definition.\n",
			task.Name, task.Driver)
		if cmdLine := taskCommandLine(task); cmdLine != "" {
			fmt.Fprintf(out, "%s\n", cmdLine)
		}
		return nil
	}

	fmt.Fprintf(cmd.ErrOrStderr(),
		"  Entrypoint script for job %q, task %q  (rendered at %s)\n\n",
		jobID, task.Name, dest)
	// Print the script verbatim — it's the payload, no decoration on stdout
	// so it can be piped to a file or a syntax highlighter.
	fmt.Fprintln(out, strings.TrimRight(script, "\n"))
	return nil
}

// selectTask returns the requested task, or the first task in the first
// group when taskFilter is empty.
func selectTask(job *NomadJob, taskFilter string) (*NomadTask, error) {
	if len(job.TaskGroups) == 0 {
		return nil, fmt.Errorf("job %q has no task groups", job.ID)
	}
	if taskFilter == "" {
		tg := job.TaskGroups[0]
		if len(tg.Tasks) == 0 {
			return nil, fmt.Errorf("job %q group %q has no tasks", job.ID, tg.Name)
		}
		return &tg.Tasks[0], nil
	}
	for gi := range job.TaskGroups {
		for ti := range job.TaskGroups[gi].Tasks {
			if job.TaskGroups[gi].Tasks[ti].Name == taskFilter {
				return &job.TaskGroups[gi].Tasks[ti], nil
			}
		}
	}
	return nil, fmt.Errorf("task %q not found in job %q", taskFilter, job.ID)
}

// extractEntrypointScript finds the embedded shell-script template for a task.
//
// Strategy:
//  1. Read the script path from config.args — the last arg that ends in ".sh"
//     (abc submits `args = [..., "${NOMAD_TASK_DIR}/<name>.sh"]`).
//  2. Match that path's basename against each template's DestPath basename.
//  3. Fall back: if exactly one template has a .sh DestPath, use it.
//
// Returns (scriptBody, destPath, true) on success.
func extractEntrypointScript(t *NomadTask) (string, string, bool) {
	if len(t.Templates) == 0 {
		return "", "", false
	}

	wantBase := scriptBasenameFromArgs(t.Config)

	// Pass 1: exact basename match against config.args.
	if wantBase != "" {
		for i := range t.Templates {
			if baseName(t.Templates[i].DestPath) == wantBase && t.Templates[i].EmbeddedTmpl != "" {
				return t.Templates[i].EmbeddedTmpl, t.Templates[i].DestPath, true
			}
		}
	}

	// Pass 2: the single .sh template, if there's exactly one.
	var shIdx []int
	for i := range t.Templates {
		if strings.HasSuffix(t.Templates[i].DestPath, ".sh") && t.Templates[i].EmbeddedTmpl != "" {
			shIdx = append(shIdx, i)
		}
	}
	if len(shIdx) == 1 {
		tp := t.Templates[shIdx[0]]
		return tp.EmbeddedTmpl, tp.DestPath, true
	}

	return "", "", false
}

// scriptBasenameFromArgs pulls the basename of the last ".sh" entry in
// config.args. config.args decodes as []interface{} (JSON array of strings).
func scriptBasenameFromArgs(config map[string]interface{}) string {
	if config == nil {
		return ""
	}
	raw, ok := config["args"]
	if !ok {
		return ""
	}
	args, ok := raw.([]interface{})
	if !ok {
		return ""
	}
	var last string
	for _, a := range args {
		s, ok := a.(string)
		if !ok {
			continue
		}
		if strings.HasSuffix(s, ".sh") {
			last = s
		}
	}
	return baseName(last)
}

// taskCommandLine renders a human-readable "command arg arg" line from
// config.command + config.args, for the no-embedded-script fallback.
func taskCommandLine(t *NomadTask) string {
	if t.Config == nil {
		return ""
	}
	var parts []string
	if cmd, ok := t.Config["command"].(string); ok && cmd != "" {
		parts = append(parts, cmd)
	}
	if raw, ok := t.Config["args"].([]interface{}); ok {
		for _, a := range raw {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, " ")
}

// baseName returns the path component after the last "/" (and strips a
// leading "${NOMAD_TASK_DIR}/" style prefix implicitly via the slash split).
func baseName(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
