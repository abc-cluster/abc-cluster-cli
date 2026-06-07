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
//   - --driver is hidden and auto-selected (raw_exec for s5cmd, exec for others)
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

  # Store at a specific path instead of the default downloads/ prefix:
  abc data fetch https://example.com/data.tar.gz \
    --destination s3://su-mbhg-hostgen/user/calm-dassie/raw/data.tar.gz

  # Fetch to a node-local directory (scratch, not persisted to storage):
  abc data fetch https://example.com/ref.fa --destination /tmp/ref/

  # Pin the job to a specific node:
  abc data fetch https://example.com/large.bam --node nomad01`,
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
  s3://...     explicit S3/MinIO URI
  /path/...    node-local directory (scratch; not persisted to MinIO)`)
	cmd.Flags().StringVar(&opts.storagePath, "storage-path", "",
		"override the S3 path when --destination storage; accepts full s3://bucket/prefix/ "+
			"or a relative path appended to the default downloads/<user>/ base")
	cmd.Flags().StringVar(&opts.tool, "tool", "aria2",
		"download tool: aria2 (default), rclone, wget, s5cmd")
	cmd.Flags().StringVar(&opts.urlFile, "url-file", "",
		"newline-separated file of URLs to fetch (alternative to positional <source>)")
	cmd.Flags().IntVar(&opts.parallel, "parallel", 4,
		"parallelism — connections per file (aria2) or worker count (s5cmd)")
	cmd.Flags().StringVar(&opts.toolArgs, "tool-args", "",
		"extra flags forwarded to the download tool verbatim (e.g. --tool-args='--no-sign-request' for s5cmd on public S3)")
	cmd.Flags().StringVar(&opts.node, "node", "",
		"pin the Nomad job to a specific node (UUID or name)")
	cmd.Flags().StringVar(&opts.name, "name", "",
		"custom Nomad job name (default: data-fetch)")

	// --driver is advanced; auto-selected but overridable by power users.
	cmd.Flags().StringVar(&opts.driver, "driver", "",
		"Nomad task driver: exec (default), raw_exec, docker, containerd "+
			"(auto-selected when blank; s5cmd → raw_exec, others → exec)")
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
		// Auto-select driver: s5cmd runs best as raw_exec; all others use exec.
		if tool == "s5cmd" {
			driver = "raw_exec"
		} else {
			driver = "exec"
		}
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

	if (driver == "docker" || driver == "containerd" || driver == "containerd-driver") && dlOpts.destination == "storage" {
		// OCI-driver jobs need a local staging dir when destination is storage.
		dlOpts.destination = "/tmp/abc-data-fetch"
		// Re-route to storage after downloading.
		dlOpts.destination = "storage"
	}

	script, err := buildToolScript(dlOpts, serverURL, accessToken, workspace, uploadEndpoint, uploadToken)
	if err != nil {
		return err
	}
	return submitJobWithDriver(cmd, dlOpts, script, driver)
}
