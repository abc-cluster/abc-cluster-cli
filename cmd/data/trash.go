package data

// trash.go — `abc data trash {list,restore,empty}` — manage soft-deleted objects.
//
// `abc data remove` moves objects to s3://<bucket>/trash/<slot>/<original-key>.
// These commands list, restore, and permanently empty that trash area.
//
//   abc data trash list                 list your trashed objects
//   abc data trash restore <trash-uri>  move an object back to its original key
//   abc data trash empty [--yes]        permanently delete everything in your trash

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func newTrashCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trash",
		Short: "List, restore, or empty soft-deleted objects (from 'abc data remove')",
		Long: `Manage objects soft-deleted with 'abc data remove'.

Removed objects live in s3://<your-group-bucket>/trash/<your-slot>/<original-key>
until the trash lifecycle rule expires them (default 30 days).`,
	}
	cmd.AddCommand(newTrashListCmd())
	cmd.AddCommand(newTrashRestoreCmd())
	cmd.AddCommand(newTrashEmptyCmd())
	return cmd
}

// trashRoot returns the s3:// URI of the active slot's trash area:
// s3://<group-bucket>/trash/<slot>/
func trashRoot(actx abccfg.Context) (uri, bucket, slot string, err error) {
	bucket = strings.TrimSpace(actx.NomadNamespace())
	if bucket == "" {
		return "", "", "", fmt.Errorf("active context has no Nomad namespace; cannot resolve your group bucket")
	}
	slot, err = slotFromCtx(actx)
	if err != nil {
		return "", "", "", err
	}
	return fmt.Sprintf("s3://%s/%s/%s/", bucket, trashPrefix, slot), bucket, slot, nil
}

func newTrashListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Aliases:      []string{"ls"},
		Short:        "List objects in your trash",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := abccfg.Load()
			if err != nil {
				return err
			}
			actx := cfg.ActiveCtx()
			root, _, _, err := trashRoot(actx)
			if err != nil {
				return err
			}
			s5, err := findTool("s5cmd")
			if err != nil {
				return err
			}
			// Recursive listing of the slot's trash root.
			return execTool(s5, s5cmdArgs(actx, "ls", []string{root + "*"}), s3Env(actx))
		},
	}
	return cmd
}

func newTrashRestoreCmd() *cobra.Command {
	var overwrite bool

	cmd := &cobra.Command{
		Use:          "restore <trash-s3-uri>...",
		Short:        "Restore object(s) from trash to their original location",
		SilenceUsage: true,
		Long: `Move object(s) from trash back to their original key.

The original key is recovered from the trash path: a trash object at
  s3://<bucket>/trash/<slot>/user/calm-dassie/results.vcf
is restored to
  s3://<bucket>/user/calm-dassie/results.vcf

Examples:

  abc data trash restore s3://su-mbhg-hostgen/trash/calm-dassie/user/calm-dassie/results.vcf`,
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
			trashKeyPrefix := fmt.Sprintf("%s/%s/", trashPrefix, slot)

			for _, trashURI := range args {
				trashURI = strings.TrimSpace(trashURI)
				bucket, trashKey, err := parseS3URI(trashURI)
				if err != nil {
					return err
				}
				if !strings.HasPrefix(trashKey, trashKeyPrefix) {
					return fmt.Errorf(
						"%q is not in your trash (expected key under %s)", trashURI, trashKeyPrefix)
				}
				// Strip trash/<slot>/ to recover the original key.
				origKey := strings.TrimPrefix(trashKey, trashKeyPrefix)
				if origKey == "" {
					return fmt.Errorf("%q has no original key to restore to", trashURI)
				}
				origURI := fmt.Sprintf("s3://%s/%s", bucket, origKey)

				if !overwrite {
					exists, err := s3ObjectExists(actx, bucket, origKey)
					if err != nil {
						return fmt.Errorf("check restore target: %w", err)
					}
					if exists {
						return fmt.Errorf(
							"%s already exists (use --overwrite to replace it on restore)", origURI)
					}
				}

				// Move back: copy then remove the trash copy.
				if err := execTool(s5, s5cmdArgs(actx, "cp", []string{trashURI, origURI}), s3Env(actx)); err != nil {
					return fmt.Errorf("restore copy failed (trash copy left intact): %w", err)
				}
				if err := execTool(s5, s5cmdArgs(actx, "rm", []string{trashURI}), s3Env(actx)); err != nil {
					return fmt.Errorf("restored but could not remove trash copy %s: %w", trashURI, err)
				}
				fmt.Fprintf(out, "restored: %s → %s\n", trashURI, origURI)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "overwrite the original location if it already exists")
	return cmd
}

func newTrashEmptyCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:          "empty",
		Short:        "Permanently delete everything in your trash",
		SilenceUsage: true,
		Long: `Permanently remove all objects in your trash. This cannot be undone.

Trash also expires automatically via the bucket lifecycle rule (default 30 days);
'empty' is for reclaiming space immediately.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := abccfg.Load()
			if err != nil {
				return err
			}
			actx := cfg.ActiveCtx()
			root, _, _, err := trashRoot(actx)
			if err != nil {
				return err
			}
			s5, err := findTool("s5cmd")
			if err != nil {
				return err
			}

			if !yes {
				fmt.Fprintf(cmd.ErrOrStderr(), "Permanently empty your trash (%s)? This cannot be undone.\n", root)
				if !confirmYesNo(cmd.ErrOrStderr(), "Type 'y' to confirm: ") {
					return fmt.Errorf("aborted")
				}
			}
			return execTool(s5, s5cmdArgs(actx, "rm", []string{root + "*"}), s3Env(actx))
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
