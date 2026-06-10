package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage an app's plain environment variables",
		Long: `View and edit an app's plain (non-secret) environment variables.

  abc app config list <name>             Show current env (AWS_* redacted)
  abc app config get <name> KEY          Print one value
  abc app config set <name> K=V [K=V...] Set var(s) and re-roll the app
  abc app config unset <name> KEY...     Remove var(s) and re-roll the app

Platform-injected vars (ABC_*, AWS_*, ABC_MINIO_ENDPOINT) are protected:
attempting to set or unset them errors. Secrets belong in a garden/Vault flow,
not here.`,
	}
	cmd.AddCommand(
		newConfigListCmd(),
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigUnsetCmd(),
	)
	return cmd
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <name>",
		Short: "Show an app's current env vars (platform vars redacted)",
		Args:  cobra.ExactArgs(1),
		RunE:  runConfigList,
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name> KEY",
		Short: "Print one env var value",
		Args:  cobra.ExactArgs(2),
		RunE:  runConfigGet,
	}
}

func newConfigSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <name> KEY=VAL [KEY=VAL...]",
		Short: "Set env var(s) and re-roll the app",
		Args:  cobra.MinimumNArgs(2),
		RunE:  runConfigSet,
	}
	cmd.Flags().Bool("no-wait", false, "Return after re-submission without polling health")
	return cmd
}

func newConfigUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <name> KEY...",
		Short: "Remove env var(s) and re-roll the app",
		Args:  cobra.MinimumNArgs(2),
		RunE:  runConfigUnset,
	}
	cmd.Flags().Bool("no-wait", false, "Return after re-submission without polling health")
	return cmd
}

// ── list / get ──────────────────────────────────────────────────────────────

func runConfigList(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	nc := nomadClientFromCmd(cmd)
	r, err := resolveApp(cmd.Context(), nc, args[0])
	if err != nil {
		return err
	}
	env := appTaskEnv(r.Job)
	if len(env) == 0 {
		fmt.Fprintf(out, "  (no env vars set for app %q)\n", r.Name)
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		val := env[k]
		// Redact platform-injected credential material; leave plain vars visible.
		if strings.HasPrefix(strings.ToUpper(k), "AWS_") {
			val = "(redacted)"
		}
		fmt.Fprintf(out, "  %s=%s\n", k, val)
	}
	return nil
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	nc := nomadClientFromCmd(cmd)
	r, err := resolveApp(cmd.Context(), nc, args[0])
	if err != nil {
		return err
	}
	key := args[1]
	env := appTaskEnv(r.Job)
	val, ok := env[key]
	if !ok {
		return fmt.Errorf("env var %q is not set for app %q", key, r.Name)
	}
	if strings.HasPrefix(strings.ToUpper(key), "AWS_") {
		val = "(redacted)"
	}
	fmt.Fprintln(out, val)
	return nil
}

// ── set / unset ─────────────────────────────────────────────────────────────

func runConfigSet(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	r, err := resolveApp(ctx, nc, args[0])
	if err != nil {
		return err
	}

	updates, err := parseEnvAssignments(args[1:])
	if err != nil {
		return err
	}

	current := appTaskEnv(r.Job)
	merged := mergeUserEnv(current, updates)

	if err := patchAndReroll(ctx, out, cmd, nc, r, merged); err != nil {
		return err
	}
	fmt.Fprintf(out, "Set %s on %s\n", strings.Join(sortedKeysOf(updates), ", "), r.Name)
	return nil
}

func runConfigUnset(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	r, err := resolveApp(ctx, nc, args[0])
	if err != nil {
		return err
	}

	keys := args[1:]
	for _, k := range keys {
		if appgen.IsProtectedEnvKey(k) {
			return protectedKeyErr(k)
		}
	}

	current := appTaskEnv(r.Job)
	merged := make(map[string]string, len(current))
	for k, v := range current {
		merged[k] = v
	}
	removed := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, ok := merged[k]; ok {
			delete(merged, k)
			removed = append(removed, k)
		}
	}
	if len(removed) == 0 {
		return fmt.Errorf("none of %v are set on app %q", keys, r.Name)
	}

	if err := patchAndReroll(ctx, out, cmd, nc, r, merged); err != nil {
		return err
	}
	fmt.Fprintf(out, "Unset %s on %s\n", strings.Join(removed, ", "), r.Name)
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

// parseEnvAssignments parses KEY=VAL tokens, rejecting protected platform keys
// and malformed assignments.
func parseEnvAssignments(tokens []string) (map[string]string, error) {
	updates := make(map[string]string, len(tokens))
	for _, t := range tokens {
		eq := strings.IndexByte(t, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("invalid assignment %q; expected KEY=VAL", t)
		}
		key := strings.TrimSpace(t[:eq])
		val := t[eq+1:]
		if key == "" {
			return nil, fmt.Errorf("invalid assignment %q; empty key", t)
		}
		if appgen.IsProtectedEnvKey(key) {
			return nil, protectedKeyErr(key)
		}
		updates[key] = val
	}
	return updates, nil
}

// mergeUserEnv returns current with updates applied. Protected keys already in
// current are preserved untouched (they cannot appear in updates — parse
// rejects them — and must survive a re-register so the platform re-injects).
func mergeUserEnv(current, updates map[string]string) map[string]string {
	out := make(map[string]string, len(current)+len(updates))
	for k, v := range current {
		out[k] = v
	}
	for k, v := range updates {
		out[k] = v
	}
	return out
}

func protectedKeyErr(key string) error {
	return fmt.Errorf(
		"%q is a platform-injected variable and cannot be set/unset via `abc app config`\n"+
			"  ABC_*, AWS_*, and ABC_MINIO_ENDPOINT are owned by the platform "+
			"(set automatically from your `project`/`data:` config)", key)
}

// appTaskEnv returns the env map of the app task from a resolved job.
func appTaskEnv(job *utils.NomadJob) map[string]string {
	if job == nil {
		return nil
	}
	for _, g := range job.TaskGroups {
		for _, t := range g.Tasks {
			if t.Name == appTaskName {
				return t.Env
			}
		}
	}
	// Fall back to the first task if the conventional name is absent.
	for _, g := range job.TaskGroups {
		for _, t := range g.Tasks {
			return t.Env
		}
	}
	return nil
}

// patchAndReroll fetches the live job JSON, replaces the app task's Env with the
// supplied map, re-registers it (a rolling update), and waits for health unless
// --no-wait. Re-registering the full job preserves every field (service tags,
// meta, checks) the typed NomadJob struct does not model.
func patchAndReroll(ctx context.Context, out io.Writer, cmd *cobra.Command, nc *utils.NomadClient, r *resolvedApp, env map[string]string) error {
	raw, err := nc.GetJobRaw(ctx, r.JobID, nc.DefaultNamespace())
	if err != nil {
		return fmt.Errorf("read job %q: %w", r.JobID, err)
	}

	var job map[string]interface{}
	if err := json.Unmarshal(raw, &job); err != nil {
		return fmt.Errorf("decode job %q: %w", r.JobID, err)
	}
	if err := setAppTaskEnv(job, env); err != nil {
		return err
	}

	patched, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("encode patched job: %w", err)
	}
	if _, err := nc.RegisterJob(ctx, patched); err != nil {
		return fmt.Errorf("re-submit app job %q: %w", r.Name, err)
	}
	fmt.Fprintf(out, "Re-rolled %s\n", r.Name)

	noWait, _ := cmd.Flags().GetBool("no-wait")
	if noWait {
		fmt.Fprintf(out, "  (--no-wait: not polling health)\n")
		return nil
	}
	health := r.Job.Meta["abc_health"]
	if health == "" {
		health = "/"
	}
	if err := waitHealthy(ctx, out, nc, r.JobID, health, defaultHealthTimeout); err != nil {
		return fmt.Errorf("app %q did not become healthy after config change: inspect `abc app logs %s`", r.Name, r.Name)
	}
	return nil
}

// setAppTaskEnv mutates the generic job map in place, replacing the Env of the
// task named "app" (falling back to the first task) within the first task group.
func setAppTaskEnv(job map[string]interface{}, env map[string]string) error {
	groups, ok := job["TaskGroups"].([]interface{})
	if !ok || len(groups) == 0 {
		return fmt.Errorf("job has no task groups")
	}
	envIface := make(map[string]interface{}, len(env))
	for k, v := range env {
		envIface[k] = v
	}
	for _, g := range groups {
		gm, ok := g.(map[string]interface{})
		if !ok {
			continue
		}
		tasks, ok := gm["Tasks"].([]interface{})
		if !ok {
			continue
		}
		// Prefer the conventional "app" task; else the first task.
		var target map[string]interface{}
		for _, tk := range tasks {
			tm, ok := tk.(map[string]interface{})
			if !ok {
				continue
			}
			if name, _ := tm["Name"].(string); name == appTaskName {
				target = tm
				break
			}
			if target == nil {
				target = tm
			}
		}
		if target != nil {
			target["Env"] = envIface
			return nil
		}
	}
	return fmt.Errorf("job has no task to patch")
}

func sortedKeysOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
