package data

// fetch.go — `abc data fetch` — download data FROM an external source INTO
// the cluster. Dispatches a Nomad job on the cluster; no local transfer occurs.
//
// This is a focused, easier-to-use front door for the most common case:
//   "I have a URL (or S3 URI) and I want the data in my MinIO bucket."
//
// Under the hood it calls the same buildToolScript + submitJobWithDriver
// machinery as `abc data download`, but with:
//   - A positional <source> argument instead of --source
//   - --destination defaults to "storage" (push to MinIO under downloads/<user>/)
//   - --tool defaults to aria2 (best general-purpose HTTP downloader)
//   - --driver is hidden and defaults to exec (member-allowed, ADR-0058), with
//     node-side tools from the abc-tools volume (ADR-0061)
//   - Nextflow pipeline mode is NOT available here (use `abc data download <accession>`,
//     which auto-detects the database and dispatches nf-core/fetchngs for SRA/ENA)

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// fetchOptions are a simplified subset of downloadOptions.
// Nextflow-specific fields are omitted deliberately.
type fetchOptions struct {
	destination string
	storagePath string
	tool        string
	driver      string
	parallel    int
	toolArgs    string
	node        string
	nodePool    string
	name        string
	urlFile     string
}

func newFetchCmd(serverURL, accessToken, workspace *string) *cobra.Command {
	opts := &fetchOptions{}

	cmd := &cobra.Command{
		Use:   "fetch <source>",
		Short: "Fetch data from a URL or storage path into cluster storage",
		Long: `Download data from an external URL or storage path directly into your cluster
storage. The download runs as a server-side job on the cluster — nothing is transferred
to your local machine.

Default behaviour:
  - Tool: aria2 (resumable HTTP; good for large files and mirrors)
  - Destination: your cluster storage under downloads/<user>/

Examples:

  # Fetch a file from the internet into your downloads folder:
  abc data fetch https://example.com/genome.fa.gz

  # Fetch from a public S3 bucket:
  abc data fetch s3://sra-pub-run-odp/sra/SRR000001/SRR000001 \
    --tool s5cmd --tool-args='--no-sign-request'

  # Fetch a URL list:
  abc data fetch --url-file urls.txt

  # Store under a specific prefix instead of the default downloads/ prefix
  # (the destination is a prefix; the file keeps its name):
  abc data fetch https://example.com/data.tar.gz \
    --destination s3://su-mbhg-hostgen/user/calm-dassie/raw/

  # Fetch to a node-local directory (scratch, not persisted to storage):
  abc data fetch https://example.com/ref.fa --destination /tmp/ref/

  # Pin the job to a specific node, or run it in another node pool:
  abc data fetch https://example.com/large.bam --node nomad01
  abc data fetch https://example.com/large.bam --node-pool platform`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && strings.TrimSpace(opts.urlFile) == "" {
				return fmt.Errorf("requires a <source> argument or --url-file")
			}
			if len(args) > 1 {
				return fmt.Errorf("accepts at most one <source> argument")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			source := ""
			if len(args) == 1 {
				source = args[0]
			}
			return runFetch(cmd, opts, source, *serverURL, *accessToken, *workspace)
		},
	}

	cmd.Flags().StringVar(&opts.destination, "destination", "storage",
		`where to store the fetched data:
  storage      push to MinIO under downloads/<user>/ (default)
  s3://...     explicit S3/MinIO prefix (same as --storage-path)
  /path/...    node-local directory (scratch; not persisted to MinIO)`)
	cmd.Flags().StringVar(&opts.storagePath, "storage-path", "",
		"override the S3 path when --destination storage; accepts full s3://bucket/prefix/ "+
			"or a relative path appended to the default downloads/<user>/ base")
	cmd.Flags().StringVar(&opts.tool, "tool", "aria2",
		"download tool: aria2 (default), rclone, wget, s5cmd")
	cmd.Flags().StringVar(&opts.urlFile, "url-file", "",
		"local newline-separated file of URLs to fetch (alternative to positional <source>); sent with the job")
	cmd.Flags().IntVar(&opts.parallel, "parallel", 4,
		"parallelism — connections per file (aria2) or worker count (s5cmd)")
	cmd.Flags().StringVar(&opts.toolArgs, "tool-args", "",
		"extra flags forwarded to the download tool verbatim (e.g. --tool-args='--no-sign-request' for s5cmd on public S3)")
	cmd.Flags().StringVar(&opts.node, "node", "",
		"pin the Nomad job to a specific node (UUID or name)")
	cmd.Flags().StringVar(&opts.nodePool, "node-pool", "",
		"Nomad node pool to run the job in (default: the context's worker pool)")
	cmd.Flags().StringVar(&opts.name, "name", "",
		"custom Nomad job name (default: data-fetch)")

	// --driver is advanced; auto-selected but overridable by power users.
	cmd.Flags().StringVar(&opts.driver, "driver", "",
		"Nomad task driver: exec (default), raw_exec, docker, containerd")
	_ = cmd.Flags().MarkHidden("driver")

	return cmd
}

func runFetch(cmd *cobra.Command, opts *fetchOptions, source, serverURL, accessToken, workspace string) error {
	tool := strings.ToLower(strings.TrimSpace(opts.tool))
	if tool == "" {
		tool = "aria2"
	}

	driver := strings.ToLower(strings.TrimSpace(opts.driver))
	if driver == "" {
		// exec for every tool. s5cmd used to default to raw_exec because an exec
		// task could not see it; it now comes from the abc-tools volume, and
		// raw_exec is refused for members.
		driver = "exec"
	}

	if tool == "nextflow" {
		return fmt.Errorf("nextflow is not supported by `abc data fetch`; use `abc data download <accession>` instead (auto-dispatches nf-core/fetchngs for SRA/ENA)")
	}

	// Map fetchOptions → downloadOptions so we share all the build/submit logic.
	dlOpts := &downloadOptions{
		source:        source,
		destination:   opts.destination,
		storagePath:   opts.storagePath,
		tool:          tool,
		driver:        driver,
		parallel:      opts.parallel,
		toolArgs:      opts.toolArgs,
		placementNode: opts.node,
		nodePool:      opts.nodePool,
		urlFile:       opts.urlFile,
		runName:       opts.name,
	}
	if dlOpts.runName == "" {
		dlOpts.runName = "data-fetch"
	}

	uploadEndpoint, err := resolveUploadEndpoint(cmd, "", serverURL)
	if err != nil {
		return err
	}
	uploadToken := resolveUploadToken(cmd, "", accessToken)

	script, err := buildToolScript(dlOpts, serverURL, accessToken, workspace, uploadEndpoint, uploadToken)
	if err != nil {
		return err
	}
	return submitJobWithDriver(cmd, dlOpts, script, driver)
}
