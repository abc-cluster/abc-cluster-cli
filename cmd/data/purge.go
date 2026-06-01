package data

// purge.go — `abc data purge` — TIER 3 complete erasure (all versions).
//
// purge removes ALL versions of an object (or all objects+versions under a
// prefix), including delete markers, via `s5cmd rm --all-versions`. This is
// the only path to true erasure on a versioned bucket and is the operation
// that satisfies GDPR/POPIA right-to-erasure.
//
// No alias — the full word `purge` must be typed, and the confirmation
// requires typing `purge` again (not just y). On an unversioned bucket,
// purge is equivalent to delete (there are no extra versions to remove).
//
// At cloud/grove tier this command will route through Khan → Jurist for a
// policy check (retention holds, erasure receipts) before executing; at
// seedling it executes directly after confirmation.

import (
	"fmt"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func newPurgeCmd() *cobra.Command {
	var yes bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:          "purge <s3-uri>...",
		Short:        "Permanently erase ALL versions of object(s) — irreversible",
		SilenceUsage: true,
		Long: `Permanently remove ALL versions of one or more objects, including delete
markers. This is complete erasure — there is no recovery and no trash.

Use this for true data erasure (e.g. a GDPR/POPIA right-to-erasure request).
For ordinary deletion prefer 'abc data remove' (trash) or 'abc data delete'
(permanent current version).

On an unversioned bucket, purge is equivalent to delete (no extra versions exist).

A confirmation requiring you to type the word 'purge' is shown unless --yes.

Examples:

  # Erase all versions of an object (prompts: type 'purge'):
  abc data purge s3://su-mbhg-hostgen/user/calm-dassie/cohort-a/sample01.vcf

  # Erase an entire prefix, all versions, no prompt:
  abc data purge s3://su-mbhg-hostgen/user/calm-dassie/cohort-a/ --yes

  # Preview:
  abc data purge s3://su-mbhg-hostgen/user/calm-dassie/cohort-a/ --dry-run`,
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

			if !yes && !dryRun {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"WARNING: purge permanently erases ALL versions of %d target(s). This cannot be undone.\n",
					len(args))
				if !confirmExactWord(cmd.ErrOrStderr(), "Type 'purge' to confirm: ", "purge") {
					return fmt.Errorf("aborted (you must type 'purge' exactly)")
				}
			}

			// --all-versions is a flag on the rm subcommand (it follows "rm"),
			// not a global flag — so it goes into rmArgs, not globalFlags.
			rmArgs := []string{"--all-versions"}
			if dryRun {
				rmArgs = append(rmArgs, "--dry-run")
			}
			rmArgs = append(rmArgs, args...)
			full := s5cmdArgs(actx, "rm", rmArgs)

			fmt.Fprintf(cmd.OutOrStdout(),
				"purging all versions of %d target(s)...\n", len(args))
			if err := execTool(s5, full, s3Env(actx)); err != nil {
				return err
			}
			// Honesty note for unversioned buckets.
			if !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"note: on an unversioned bucket this removed the single stored copy "+
						"(equivalent to delete). Enable versioning via "+
						"'abc operator minio setup-bucket' for version-aware erasure.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the typed confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be purged without erasing")
	return cmd
}
