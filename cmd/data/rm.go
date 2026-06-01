package data

// rm.go — `abc data remove` (alias: rm) — TIER 1 soft delete to trash.
//
// remove moves an object to the group bucket's trash/ prefix instead of
// permanently deleting it. It is reversible via `abc data trash restore`
// for as long as the trash lifecycle rule retains it (default 30 days).
//
// Three-tier deletion model (design/exploring nothing — see
// brainstorms/abc-data-platform/2026-05-30-deletion-semantics.md):
//   remove (rm)  — TIER 1: soft delete to trash/ (reversible)         ← this file
//   delete (del) — TIER 2: permanent, current version, with confirm   ← delete.go
//   purge        — TIER 3: all versions, typed confirmation           ← purge.go
//
// Trash key layout (preserves the original key so restore is unambiguous):
//   s3://<bucket>/trash/<slot>/<original-key>
//
// Collision (unversioned bucket): if the trash target already exists, fail
// unless --overwrite. (On a versioned bucket each write is a new version, so
// collision is handled for free — but versioning is off at seedling today.)

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// trashPrefix is the bucket-relative prefix under which removed objects are kept.
const trashPrefix = "trash"

func newRemoveCmd() *cobra.Command {
	var overwrite bool

	cmd := &cobra.Command{
		Use:          "remove <s3-uri>...",
		Aliases:      []string{"rm"},
		Short:        "Soft-delete object(s) to your group's trash/ (reversible)",
		SilenceUsage: true,
		Long: `Move one or more objects to your group bucket's trash/ prefix.

remove is reversible: objects land in trash/<your-slot>/<original-key> and can
be restored with 'abc data trash restore' until the trash lifecycle rule expires
them (default 30 days). This is the safe default deletion.

For permanent deletion, use:
  abc data delete <s3-uri>   permanent, current version (with confirmation)
  abc data purge  <s3-uri>   permanent, ALL versions (typed confirmation)

Examples:

  # Move a file to trash (recoverable):
  abc data remove s3://su-mbhg-hostgen/user/calm-dassie/old.vcf

  # rm alias:
  abc data rm s3://su-mbhg-hostgen/user/calm-dassie/old.vcf

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
					return fmt.Errorf("removed-to-trash but could not delete original %s: %w", srcURI, err)
				}
				fmt.Fprintf(out, "trashed: %s → %s\n", srcURI, trashURI)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing object of the same name already in trash")
	return cmd
}

// slotFromCtx resolves the active pool slot identity from auth.whoami.
// Shared by remove / trash. Returns a helpful error if whoami is unset.
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
