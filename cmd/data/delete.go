package data

// delete.go — `abc data delete` (alias: del) — TIER 1 soft delete to trash.
//
// delete is the recoverable, safe-by-default deletion: objects move to the
// group bucket's trash/ prefix instead of being permanently removed, and can be
// restored with `abc data trash restore` for as long as the trash lifecycle
// rule retains them (default 30 days). This matches the desktop-GUI
// "move to Trash" convention. For permanent removal use `abc data remove`
// (Unix rm), or `abc data purge` for all versions.
//
// See brainstorms/abc-data-platform/2026-06-05-invert-remove-delete.md, which
// supersedes the 2026-05-30 naming by swapping remove/delete.
//
// Trash key layout (preserves the original key so restore is unambiguous):
//   s3://<bucket>/trash/<slot>/<original-key>
//
// Collision (unversioned bucket): if the trash target already exists, fail
// unless --overwrite. (On a versioned bucket each write is a new version, so
// collision is handled for free — but versioning is off at seedling today.)

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func newDeleteCmd() *cobra.Command {
	var overwrite bool

	cmd := &cobra.Command{
		Use:          "delete <s3-uri>...",
		Aliases:      []string{"del"},
		Short:        "Recoverable delete — move object(s) to your group's trash/ (reversible)",
		SilenceUsage: true,
		Long: `Move one or more objects to your group bucket's trash/ prefix.

delete is reversible: objects land in trash/<your-slot>/<original-key> and can
be restored with 'abc data trash restore' until the trash lifecycle rule expires
them (default 30 days). This is the safe default deletion.

For permanent removal, use:
  abc data remove <s3-uri>   permanent, current version (with confirmation)
  abc data purge  <s3-uri>   permanent, ALL versions (typed confirmation)

Examples:

  # Move a file to trash (recoverable):
  abc data delete s3://su-mbhg-hostgen/user/calm-dassie/old.vcf

  # del alias:
  abc data del s3://su-mbhg-hostgen/user/calm-dassie/old.vcf

  # See what's in your trash, then restore:
  abc data trash list
  abc data trash restore s3://su-mbhg-hostgen/trash/calm-dassie/user/calm-dassie/old.vcf`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := abccfg.Load()
			if err != nil {
				return err
			}
			actx := cfg.ActiveCtx()

			slot, err := slotFromCtx(actx)
			if err != nil {
				return err
			}

			s5, err := findTool("s5cmd")
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			for _, srcURI := range args {
				srcURI = strings.TrimSpace(srcURI)
				bucket, key, err := parseS3URI(srcURI)
				if err != nil {
					return err
				}
				if key == "" {
					return fmt.Errorf("source must include an object key: %q", srcURI)
				}
				if strings.HasPrefix(key, trashPrefix+"/") {
					return fmt.Errorf("%q is already in trash; use 'abc data trash restore' or 'abc data purge' instead", srcURI)
				}

				trashKey := fmt.Sprintf("%s/%s/%s", trashPrefix, slot, key)
				trashURI := fmt.Sprintf("s3://%s/%s", bucket, trashKey)

				// Collision guard (unversioned bucket).
				if !overwrite {
					exists, err := s3ObjectExists(actx, bucket, trashKey)
					if err != nil {
						return fmt.Errorf("check trash target: %w", err)
					}
					if exists {
						return fmt.Errorf(
							"%s already exists in trash (use --overwrite, or restore the existing copy first)",
							trashURI)
					}
				}

				// Move = server-side copy to trash, then remove the original.
				if err := execTool(s5, s5cmdArgs(actx, "cp", []string{srcURI, trashURI}), s3Env(actx)); err != nil {
					return fmt.Errorf("copy to trash failed (original left intact): %w", err)
				}
				if err := execTool(s5, s5cmdArgs(actx, "rm", []string{srcURI}), s3Env(actx)); err != nil {
					return fmt.Errorf("deleted-to-trash but could not remove original %s: %w", srcURI, err)
				}
				fmt.Fprintf(out, "trashed: %s → %s\n", srcURI, trashURI)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing object of the same name already in trash")
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
