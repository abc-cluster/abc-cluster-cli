package data

// pull.go — `abc data pull` — download data FROM cluster MinIO TO the user's
// local machine. Runs s5cmd locally; no Nomad job is submitted.
//
// This is the read path for getting results off the cluster:
//   "I ran a pipeline, the output is in MinIO, I want it on my laptop."
//
// The local s5cmd binary is used because:
//   - It is fast (parallel, multi-part)
//   - --if-checksum-differ makes every run resumable and idempotent
//   - There is no server-side job latency or quota impact

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	var destination string
	var parallel int

	cmd := &cobra.Command{
		Use:   "pull <s3-uri>",
		Short: "Download a file or prefix from cluster MinIO to your local machine",
		Long: `Download data from your cluster MinIO bucket to the local machine using s5cmd.

Downloads are resumable and checksum-verified: files whose local checksum
matches the remote are skipped automatically (--if-checksum-differ). This
means you can safely re-run the command after an interrupted download or to
sync any newly added objects.

Requires s5cmd to be available locally. If it is not in your PATH, install
it from https://github.com/peak/s5cmd/releases.

Examples:

  # Download a single file to the current directory:
  abc data pull s3://su-mbhg-hostgen/user/calm-dassie/data/results.csv

  # Download to a specific local directory:
  abc data pull s3://su-mbhg-hostgen/user/calm-dassie/data/results.csv \
    --destination ~/downloads/

  # Download an entire prefix (trailing / triggers recursive copy):
  abc data pull s3://su-mbhg-hostgen/user/calm-dassie/results/ \
    --destination ./run-outputs/

  # Use more parallel workers for large transfers:
  abc data pull s3://su-mbhg-hostgen/user/calm-dassie/data/ \
    --destination ./data/ --parallel 8`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := strings.TrimSpace(args[0])
			if !strings.HasPrefix(strings.ToLower(src), "s3://") {
				return fmt.Errorf("source must be an S3 URI (s3://...); got %q\n"+
					"  To download from the internet, use: abc data fetch <url>", src)
			}
			return LocalFetchFromS3(cmd, src, destination, parallel)
		},
	}

	cmd.Flags().StringVarP(&destination, "destination", "d", "",
		"local directory to download into (default: current working directory)")
	cmd.Flags().IntVarP(&parallel, "parallel", "p", 4,
		"number of parallel s5cmd workers")

	return cmd
}
