package data

// rm.go — `abc data remove` (alias: rm) — permanent deletion of the current
// version. Aligned with the Unix `rm` convention: remove is the DESTRUCTIVE
// verb. For a recoverable delete, use `abc data delete` (soft delete to trash).
//
// Three-tier deletion model (see
// brainstorms/abc-data-platform/2026-06-05-invert-remove-delete.md, which
// supersedes the 2026-05-30 naming by swapping remove/delete):
//   delete (del) — TIER 1: soft delete to trash/ (reversible)         ← delete.go
//   remove (rm)  — TIER 2: permanent, current version, with confirm   ← this file
//   purge        — TIER 3: all versions, typed confirmation           ← purge.go
//
// trashPrefix and slotFromCtx are defined here but used by the soft-delete
// command in delete.go (package-scoped — same `package data`).

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// trashPrefix is the bucket-relative prefix under which soft-deleted objects are kept.
const trashPrefix = "trash"

func newRemoveCmd() *cobra.Command {
	var yes bool
	var recursive bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:          "remove <s3-uri>...",
		Aliases:      []string{"rm"},
		Short:        "Permanently remove object(s) — current version (with confirmation)",
		SilenceUsage: true,
		Long: `Permanently remove one or more objects. This does NOT go through trash.

remove follows the Unix convention — it is the destructive verb. For a
recoverable delete, use 'abc data delete' (soft delete to trash). To remove
ALL versions of an object, use 'abc data purge'.

A confirmation prompt is shown unless --yes is passed.

Examples:

  # Remove an object permanently (prompts for confirmation):
  abc data remove s3://su-mbhg-hostgen/user/calm-dassie/scratch.bam

  # rm alias, whole prefix, no prompt:
  abc data rm s3://su-mbhg-hostgen/user/calm-dassie/tmp/ --recursive --yes

  # Preview what would be removed:
  abc data remove s3://su-mbhg-hostgen/user/calm-dassie/tmp/ --recursive --dry-run`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := abccfg.Load()
			if err != nil {
				return err
			}
			actx := cfg.ActiveCtx()

			s5, err := findTool("s5cmd")
			if err != nil {
				return err
			}

			rmArgs := make([]string, 0, len(args)+1)
			if dryRun {
				rmArgs = append(rmArgs, "--dry-run")
			}
			rmArgs = append(rmArgs, args...)

			if !yes && !dryRun {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Permanently remove %d target(s)? This cannot be undone (use 'abc data delete' for a recoverable delete).\n",
					len(args))
				if !confirmYesNo(cmd.ErrOrStderr(), "Type 'y' to confirm: ") {
					return fmt.Errorf("aborted")
				}
			}

			return execTool(s5, s5cmdArgs(actx, "rm", rmArgs), s3Env(actx))
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&recursive, "recursive", false, "remove all objects under a prefix (use a trailing /* with s5cmd globbing)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed without removing")
	return cmd
}

// slotFromCtx resolves the active pool slot identity from auth.whoami.
// Used by the soft-delete command (delete.go) and trash. Returns a helpful
// error if whoami is unset.
func slotFromCtx(actx abccfg.Context) (string, error) {
	if actx.Auth == nil {
		return "", fmt.Errorf("active context has no auth block; run `abc auth whoami` first")
	}
	slot := utils.WhoamiPathSegment(actx.Auth.Whoami)
	if slot == "" {
		return "", fmt.Errorf("could not resolve your slot identity from auth.whoami (run `abc auth whoami`)")
	}
	return slot, nil
}
