package data

// delete.go — `abc data delete` (alias: del) — TIER 2 permanent deletion.
//
// delete permanently removes the current version of the object(s). It does NOT
// go through trash. On a versioned bucket it removes the current version-id
// (not just a delete marker) — users who type `delete` expect permanent removal.
//
// Requires confirmation unless --yes. See the three-tier model in
// brainstorms/abc-data-platform/2026-05-30-deletion-semantics.md.

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func newDeleteCmd() *cobra.Command {
	var yes bool
	var recursive bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:          "delete <s3-uri>...",
		Aliases:      []string{"del"},
		Short:        "Permanently delete object(s) — current version (with confirmation)",
		SilenceUsage: true,
		Long: `Permanently delete one or more objects. This does NOT go through trash.

For a recoverable delete, use 'abc data remove' (soft delete to trash).
To remove ALL versions of an object, use 'abc data purge'.

A confirmation prompt is shown unless --yes is passed.

Examples:

  # Delete an object permanently (prompts for confirmation):
  abc data delete s3://su-mbhg-hostgen/user/calm-dassie/scratch.bam

  # Delete a whole prefix without prompting:
  abc data delete s3://su-mbhg-hostgen/user/calm-dassie/tmp/ --recursive --yes

  # Preview what would be deleted:
  abc data delete s3://su-mbhg-hostgen/user/calm-dassie/tmp/ --recursive --dry-run`,
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
					"Permanently delete %d target(s)? This cannot be undone (use 'abc data remove' for a recoverable delete).\n",
					len(args))
				if !confirmYesNo(cmd.ErrOrStderr(), "Type 'y' to confirm: ") {
					return fmt.Errorf("aborted")
				}
			}

			return execTool(s5, s5cmdArgs(actx, "rm", rmArgs), s3Env(actx))
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&recursive, "recursive", false, "delete all objects under a prefix (use a trailing /* with s5cmd globbing)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be deleted without deleting")
	return cmd
}

// confirmYesNo prompts on the given writer and reads a line from stdin.
// Returns true only if the response is exactly y or yes (case-insensitive).
func confirmYesNo(promptW interface{ Write([]byte) (int, error) }, prompt string) bool {
	fmt.Fprint(promptW, prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	resp := strings.ToLower(strings.TrimSpace(line))
	return resp == "y" || resp == "yes"
}

// confirmExactWord prompts and requires the response to equal want exactly.
// Used by purge (must type 'purge') as a friction mechanism.
func confirmExactWord(promptW interface{ Write([]byte) (int, error) }, prompt, want string) bool {
	fmt.Fprint(promptW, prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line) == want
}
