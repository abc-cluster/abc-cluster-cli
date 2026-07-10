package data

// push.go — `abc data push`
//
// Upload a local file or directory to MinIO/S3 using s5cmd.
// The fast S3-native sibling of `abc data upload` (which uses tus).
//
// When to use push vs upload:
//   push   — fast network (on-cluster, VPN, campus), large files, no tus tracking needed
//   upload — unreliable network; want progress tracked across CLI restarts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newPushCmd() *cobra.Command {
	var parallel int
	var checksum bool
	var dryRun bool
	var compress string

	cmd := &cobra.Command{
		Use:          "push <local-path> <s3-uri>",
		Short:        "Upload a local file or directory to storage (fast, no tracking)",
		SilenceUsage: true,
		Long: `Upload a local file or directory to cluster MinIO storage using s5cmd.

push is the fast, S3-native upload path — it uses s5cmd cp with parallel
multipart transfers. No upload tracking is maintained (use 'abc data upload'
if you need resumable tracked uploads over unreliable networks).

push and pull are symmetric:
  push  local → storage   (this command)
  pull  storage → local   (abc data pull)

Examples:

  # Upload a single file:
  abc data push ./genome.fa s3://su-mbhg-hostgen/user/calm-dassie/ref/genome.fa

  # Upload a directory (trailing / on src = contents only):
  abc data push ./results/ s3://su-mbhg-hostgen/user/calm-dassie/results/

  # Skip files whose checksum matches the remote ETag (idempotent re-push):
  abc data push ./results/ s3://su-mbhg-hostgen/user/calm-dassie/results/ --checksum

  # Dry-run to preview:
  abc data push ./results/ s3://su-mbhg-hostgen/user/calm-dassie/results/ --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			localPath := args[0]
			s3URI := args[1]

			if !strings.HasPrefix(strings.ToLower(s3URI), "s3://") {
				return fmt.Errorf("destination must be an S3 URI (s3://...); got %q", s3URI)
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}

			bin, err := findTool("s5cmd")
			if err != nil {
				return fmt.Errorf("%w\n\nAlternative: use 'abc data upload' for tracked uploads", err)
			}

			// --compress: zstd-compress a raw file to a temp before pushing.
			// Directories are not supported (compress first, then push). Already-
			// compressed inputs push as-is.
			if compress != "" {
				info, statErr := os.Stat(localPath)
				if statErr != nil {
					return statErr
				}
				if info.IsDir() {
					return fmt.Errorf("--compress does not support directories; run 'abc data compress %s' first, then 'abc data push' the .zst files", localPath)
				}
				kind, cErr := classifyCompression(localPath)
				if cErr != nil {
					return cErr
				}
				if kind != kindRaw {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: %s is already %s — pushing as-is (not recompressed)\n", filepath.Base(localPath), kind)
				} else {
					comp, cErr := newCompressConfig(compress)
					if cErr != nil {
						return cErr
					}
					tmpPath, cleanup, _, _, cErr := comp.compressToTempFileWithProgress(cmd.Context(), localPath, nil)
					if cErr != nil {
						return fmt.Errorf("compression failed: %w", cErr)
					}
					defer func() { _ = cleanup() }()
					if strings.HasSuffix(s3URI, "/") {
						s3URI = s3URI + filepath.Base(localPath) + zstdDefaultSuffix
					} else {
						s3URI = s3URI + zstdDefaultSuffix
					}
					localPath = tmpPath
				}
			}

			// Directory sources upload recursively via s5cmd. Normalize a dir push
			// to the documented "contents into prefix" form (dir/ → prefix/) by
			// appending trailing slashes to both sides. Without this, pushing a dir
			// without exact trailing slashes either errored ("target must be a
			// bucket or a prefix") or silently nested the dir name under itself —
			// the footgun that made directory push look unsupported (B15). Now
			// `abc data push ./results s3://.../results` recursively uploads the
			// contents into .../results/ predictably.
			if info, statErr := os.Stat(localPath); statErr == nil && info.IsDir() {
				orig := localPath
				localPath, s3URI = normalizePushDir(localPath, s3URI)
				fmt.Fprintf(cmd.ErrOrStderr(), "note: %s is a directory — uploading its contents recursively into %s\n", strings.TrimRight(orig, "/"), s3URI)
			}

			// Build s5cmd invocation. --numworkers is a global flag (before the
			// subcommand); cp flags are built by buildPushCpArgs and precede the
			// positionals (see that function for why ordering matters).
			var globalFlags []string
			if parallel > 0 {
				globalFlags = append(globalFlags, "--numworkers", fmt.Sprintf("%d", parallel))
			}

			cpArgs := buildPushCpArgs(checksum, dryRun, localPath, s3URI)

			return execTool(bin, s5cmdArgs(cfg.ActiveCtx(), "cp", cpArgs, globalFlags...), s3Env(cfg.ActiveCtx()))
		},
	}

	cmd.Flags().IntVar(&parallel, "parallel", 4, "number of parallel s5cmd workers")
	cmd.Flags().BoolVar(&checksum, "checksum", false, "skip files whose size already matches the remote object — idempotent re-push (s5cmd --if-size-differ)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be uploaded without transferring")
	cmd.Flags().StringVar(&compress, "compress", "", "zstd-compress a raw file before push (level: fast|default|better|best); already-compressed files push as-is, directories are not supported")
	cmd.Flags().Lookup("compress").NoOptDefVal = "default"

	return cmd
}

// normalizePushDir returns the (src, dst) for pushing a local *directory*: it
// uploads the directory's CONTENTS into the destination prefix (rsync-style) by
// ensuring both paths end with "/". This gives predictable recursive directory
// push regardless of how the user wrote the trailing slashes — instead of the
// s5cmd "target must be a bucket or a prefix" error, or silently nesting the
// directory name under itself (B15).
func normalizePushDir(localPath, s3URI string) (string, string) {
	if !strings.HasSuffix(localPath, "/") {
		localPath += "/"
	}
	if !strings.HasSuffix(s3URI, "/") {
		s3URI += "/"
	}
	return localPath, s3URI
}

// buildPushCpArgs builds the s5cmd `cp` argument list (everything after the
// `cp` subcommand) for a push: any flags FIRST, then the source/destination
// positionals.
//
// Ordering is load-bearing: s5cmd uses urfave/cli, which stops parsing flags at
// the first positional argument. A flag placed after <src> <dst> is treated as a
// bogus third positional and s5cmd fails with "expected source and destination
// arguments". (This was bug G-D.)
//
// The skip-if-unchanged flag is `--if-size-differ` — s5cmd has NO checksum-based
// skip flag (`--if-checksum-differ` does not exist; that was the root cause of
// bugs G-D and G-E). `--if-size-differ` gives idempotent, resumable re-pushes for
// immutable objects and matches LocalPushToS3 / LocalFetchFromS3.
func buildPushCpArgs(checksum, dryRun bool, localPath, s3URI string) []string {
	var cpArgs []string
	if checksum {
		cpArgs = append(cpArgs, "--if-size-differ")
	}
	if dryRun {
		cpArgs = append(cpArgs, "--dry-run")
	}
	cpArgs = append(cpArgs, localPath, s3URI)
	return cpArgs
}
