package data

// share.go — `abc data share`
//
// Server-side copy of a file from the user's area into the group's
// `shared/` or `common/` prefix. The bind-mounted `~/shared/` and
// `~/common/` paths inside JupyterLab sessions are read-only (geesefs
// + `bind,ro`); writes bypass FUSE and go directly to MinIO via s5cmd.
//
// Path conventions (per `brainstorms/abc-data-platform/2026-05-30-geesefs-deployment-and-share-design.md`):
//
//   shared/<your-slot>/<filename>    each slot has its own subdir
//   common/<filename>                flat; admin-only (MinIO IAM gates)
//
// v1 is intra-group only: the source URI's bucket must equal the user's
// own group bucket (the active context's Nomad namespace). Cross-group
// sharing is deliberately out of scope at seedling — use `abc data
// presign` to hand out a time-limited URL instead.
//
// Tool resolution: findTool("s5cmd"); the copy is server-side because
// MinIO honours CopyObject when source + destination share the endpoint.

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

func newShareCmd() *cobra.Command {
	var to string
	var asName string
	var overwrite bool

	cmd := &cobra.Command{
		Use:          "share <s3-uri>",
		Short:        "Server-side copy a file into your group's shared/ (or common/, admin only)",
		SilenceUsage: true,
		Long: `Server-side copy a file from your area to the group's shared/ or common/ prefix.

The file appears in all group members' ~/shared/ (or ~/common/) bind mount
within ~10s (geesefs stat-cache-ttl). Writes bypass the FUSE read-only mount
and go through MinIO directly via s5cmd CopyObject.

Path conventions:
  shared/<your-slot>/<filename>    each slot has its own subdir, preserving provenance
  common/<filename>                flat; admin only (MinIO IAM denies non-admins)

Intra-group only at v1: the source must be in your own group bucket. For
cross-group transfer, use abc data presign to hand out a time-limited URL.

Examples:

  # Share a result file with your group:
  abc data share s3://su-mbhg-hostgen/user/slot-calm-dassie/results.vcf --to shared

  # Share with a custom name / sub-path under your slot's shared/ folder:
  abc data share s3://su-mbhg-hostgen/user/slot-calm-dassie/refs.fa \
      --to shared --as references/grch38.fa

  # Replace an existing file (default refuses to overwrite):
  abc data share s3://su-mbhg-hostgen/user/slot-calm-dassie/results.vcf \
      --to shared --overwrite

  # Publish to the whole cluster (admin only):
  abc data share s3://su-mbhg-hostgen/user/slot-calm-dassie/release.tar --to common
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// --- user-input validation first (no env lookups) ---
			to = strings.ToLower(strings.TrimSpace(to))
			if to != "shared" && to != "common" {
				return fmt.Errorf("--to must be 'shared' or 'common', got %q", to)
			}

			srcURI := args[0]
			srcBucket, srcKey, err := parseS3URI(srcURI)
			if err != nil {
				return err
			}
			if srcKey == "" {
				return fmt.Errorf("source must include an object key (s3://bucket/path/to/file)")
			}

			// Derive + validate destination filename (pure input, no env).
			destName := strings.TrimSpace(asName)
			if destName == "" {
				destName = path.Base(srcKey)
			}
			if !isValidShareName(destName) {
				return fmt.Errorf("--as %q is not a valid relative path (no absolute paths, no '..' segments)", asName)
			}

			// --- env-dependent: load config, resolve group bucket ---
			cfg, err := abccfg.Load()
			if err != nil {
				return err
			}
			actx := cfg.ActiveCtx()

			myBucket := strings.TrimSpace(actx.NomadNamespace())
			if myBucket == "" {
				return fmt.Errorf("active context has no Nomad namespace; cannot resolve your group bucket")
			}
			if srcBucket != myBucket {
				return fmt.Errorf(
					"source bucket %q ≠ your group bucket %q; cross-group share is not supported at seedling (use `abc data presign` for cross-group transfers)",
					srcBucket, myBucket,
				)
			}

			// Build destination key.
			var destKey string
			switch to {
			case "shared":
				if actx.Auth == nil {
					return fmt.Errorf("active context has no auth block; run `abc auth whoami` first")
				}
				slot := utils.WhoamiPathSegment(actx.Auth.Whoami)
				if slot == "" {
					return fmt.Errorf("could not resolve your slot identity from auth.whoami (run `abc auth whoami` to populate it)")
				}
				destKey = fmt.Sprintf("shared/%s/%s", slot, destName)
			case "common":
				destKey = fmt.Sprintf("common/%s", destName)
			}
			destURI := fmt.Sprintf("s3://%s/%s", myBucket, destKey)

			if srcKey == destKey {
				return fmt.Errorf("source equals destination (%s); nothing to do", destURI)
			}

			// Pre-check: refuse to clobber unless --overwrite.
			if !overwrite {
				exists, err := s3ObjectExists(actx, myBucket, destKey)
				if err != nil {
					return fmt.Errorf("check destination: %w", err)
				}
				if exists {
					return fmt.Errorf("destination %s already exists (use --overwrite to replace)", destURI)
				}
			}

			// Server-side copy via s5cmd (CopyObject under the hood — same endpoint).
			s5, err := findTool("s5cmd")
			if err != nil {
				return err
			}
			s5Args := s5cmdArgs(actx, "cp", []string{srcURI, destURI})
			if err := execTool(s5, s5Args, s3Env(actx)); err != nil {
				if to == "common" {
					return fmt.Errorf("share to common failed: %w\n(writing to common/ requires group-admin role — non-admins are denied by MinIO IAM)", err)
				}
				return fmt.Errorf("share copy failed: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "shared: %s → %s\n", srcURI, destURI)
			return nil
		},
	}

	cmd.Flags().StringVar(&to, "to", "", "Destination scope: 'shared' (group members) or 'common' (whole cluster, admin only)")
	cmd.Flags().StringVar(&asName, "as", "", "Override the destination filename (relative path under the slot's subdir)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite an existing destination object")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

// isValidShareName rejects empty, absolute, or traversing paths. It allows
// nested sub-paths (e.g. "references/refs.fa") so users can organise their
// shared area.
func isValidShareName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// s3ObjectExists returns true iff the object exists in MinIO. Uses s5cmd ls
// silently (any non-zero exit is interpreted as "not present"). Errors only
// for genuine plumbing failures (tool missing, etc.).
func s3ObjectExists(ctx abccfg.Context, bucket, key string) (bool, error) {
	s5, err := findTool("s5cmd")
	if err != nil {
		return false, err
	}
	uri := fmt.Sprintf("s3://%s/%s", bucket, key)
	args := s5cmdArgs(ctx, "ls", []string{uri})
	c := exec.Command(s5, args...)
	c.Env = append(os.Environ(), s3Env(ctx)...)
	c.Stdout = nil
	c.Stderr = nil
	if err := c.Run(); err != nil {
		if _, isExit := err.(*exec.ExitError); isExit {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
