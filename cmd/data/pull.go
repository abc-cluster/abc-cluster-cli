package data

// pull.go — `abc data pull` — download data FROM cluster MinIO TO the user's
// local machine. Runs s5cmd locally; no Nomad job is submitted.
//
// This is the read path for getting results off the cluster:
//   "I ran a pipeline, the output is in MinIO, I want it on my laptop."
//
// The local s5cmd binary is used because:
//   - It is fast (parallel, multi-part)
//   - --if-size-differ makes every run resumable and idempotent
//   - There is no server-side job latency or quota impact

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	var destination string
	var parallel int
	var decompress bool

	cmd := &cobra.Command{
		Use:   "pull <s3-uri> [<local-dest>]",
		Short: "Download a file or prefix from cluster storage to your local machine",
		Long: `Download data from your cluster MinIO bucket to the local machine using s5cmd.

Downloads are resumable and idempotent: files whose local size already
matches the remote are skipped automatically (--if-size-differ). This
means you can safely re-run the command after an interrupted download or to
sync any newly added objects.

Requires s5cmd to be available locally. If it is not in your PATH, install
it from https://github.com/peak/s5cmd/releases.

Examples:

  # Download a single file to the current directory:
  abc data pull s3://su-mbhg-hostgen/user/calm-dassie/data/results.csv

  # Download to a specific local directory (positional, symmetric with push):
  abc data pull s3://su-mbhg-hostgen/user/calm-dassie/data/results.csv ~/downloads/

  # Equivalent, via the flag:
  abc data pull s3://su-mbhg-hostgen/user/calm-dassie/data/results.csv -d ~/downloads/

  # Download an entire prefix (trailing / triggers recursive copy):
  abc data pull s3://su-mbhg-hostgen/user/calm-dassie/results/ ./run-outputs/

  # Use more parallel workers for large transfers:
  abc data pull s3://su-mbhg-hostgen/user/calm-dassie/data/ ./data/ --parallel 8`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := strings.TrimSpace(args[0])
			if !strings.HasPrefix(strings.ToLower(src), "s3://") {
				return fmt.Errorf("source must be an S3 URI (s3://...); got %q\n"+
					"  To download from the internet, use: abc data fetch <url>", src)
			}
			// Accept the destination positionally too, for symmetry with
			// `push <local> <s3>` (B15). --destination stays supported.
			dest, err := resolvePullDest(args, destination)
			if err != nil {
				return err
			}
			destination = dest
			if err := LocalFetchFromS3(cmd, src, destination, parallel); err != nil {
				return err
			}
			if decompress {
				return decompressPulled(cmd, src, destination)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&destination, "destination", "d", "",
		"local directory to download into (default: current working directory)")
	cmd.Flags().IntVarP(&parallel, "parallel", "p", 4,
		"number of parallel s5cmd workers")
	cmd.Flags().BoolVar(&decompress, "decompress", false,
		"after download, expand any .zst artifacts locally (integrity-verified); non-zstd files are left untouched")

	return cmd
}

// resolvePullDest picks the local download destination from an optional second
// positional arg and the --destination flag. The positional form makes pull
// symmetric with `push <local> <s3>`. Supplying both is an error unless they
// agree; supplying neither returns "" (LocalFetchFromS3 defaults to cwd).
func resolvePullDest(args []string, flagDest string) (string, error) {
	if len(args) < 2 {
		return flagDest, nil
	}
	pos := strings.TrimSpace(args[1])
	if flagDest != "" && flagDest != pos {
		return "", fmt.Errorf("destination given both positionally (%q) and via --destination (%q) — use one", pos, flagDest)
	}
	return pos, nil
}

// decompressPulled expands .zst artifacts after a pull. For a single-object pull
// it targets the one downloaded file; for a prefix pull it walks the destination
// directory. Each .zst is decompressed (integrity-verified) and removed; a verify
// failure aborts without deleting the .zst.
func decompressPulled(cmd *cobra.Command, src, destination string) error {
	destDir := destination
	if destDir == "" {
		destDir = "."
	}
	if !strings.HasSuffix(src, "/") {
		base := path.Base(strings.TrimRight(strings.TrimPrefix(src, "s3://"), "/"))
		target := filepath.Join(destDir, base)
		if strings.HasSuffix(target, zstdDefaultSuffix) {
			return decompressInPlace(cmd, target)
		}
		return nil
	}
	return filepath.WalkDir(destDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, zstdDefaultSuffix) {
			return nil
		}
		return decompressInPlace(cmd, p)
	})
}

func decompressInPlace(cmd *cobra.Command, zstPath string) error {
	out := strings.TrimSuffix(zstPath, zstdDefaultSuffix)
	comp := &compressConfig{}
	err := writeWithCollisionCheck(out, true, cmd.ErrOrStderr(),
		func(p string) error {
			_, derr := comp.decompressToPath(cmd.Context(), zstPath, p, nil)
			return derr
		})
	if err != nil {
		return fmt.Errorf("decompress %q (left in place): %w", zstPath, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Decompressed %s -> %s\n", zstPath, out)
	if err := os.Remove(zstPath); err != nil {
		return fmt.Errorf("remove %q after decompress: %w", zstPath, err)
	}
	return nil
}
