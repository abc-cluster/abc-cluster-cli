package data

import (
	"context"

	"github.com/abc-cluster/abc-cluster-cli/api"
	"github.com/spf13/cobra"
)

// UploaderOptions configures optional behaviour of the tus uploader.
type UploaderOptions struct {
	ChunkSize int64 // 0 = tusgo default (~2 MiB)
	MaxRate   int64 // bytes/sec; 0 = unlimited
	NoResume  bool  // always start a fresh upload, ignoring stored state
}

// Uploader uploads files to a tus endpoint.
type Uploader interface {
	Upload(ctx context.Context, filePath string, metadata map[string]string) (string, error)
}

// ClientFactory creates an Uploader from connection parameters and options.
type ClientFactory func(endpoint, accessToken string, opts UploaderOptions) (Uploader, error)

// defaultClientFactory creates a real tus uploader.
func defaultClientFactory(endpoint, accessToken string, opts UploaderOptions) (Uploader, error) {
	return newTusUploader(endpoint, accessToken, opts)
}

// PipelineRunner is the interface for submitting a pipeline run.
type PipelineRunner interface {
	SubmitPipelineRun(req *api.PipelineRunRequest) (*api.PipelineRunResponse, error)
}

// PipelineClientFactory creates a PipelineRunner from connection parameters.
type PipelineClientFactory func(serverURL, accessToken, workspace string) PipelineRunner

// defaultPipelineClientFactory creates a real API client.
func defaultPipelineClientFactory(serverURL, accessToken, workspace string) PipelineRunner {
	return api.NewClient(serverURL, accessToken, workspace)
}

// PipelineFactory is used by data download command and can be replaced in tests.
var PipelineFactory = defaultPipelineClientFactory

// NewCmd returns the "data" subcommand group.
func NewCmd(serverURL, accessToken, workspace *string, dataFactory ...ClientFactory) *cobra.Command {
	f := defaultClientFactory
	if len(dataFactory) > 0 && dataFactory[0] != nil {
		f = dataFactory[0]
	}

	cmd := &cobra.Command{
		Use:   "data",
		Short: "Manage data",
		Long: `Commands for the full data lifecycle on abc-cluster.

Common workflows:

  Upload a file to cluster MinIO (tus, resumable):
    abc data upload ./genome.fa

  Push a file to MinIO fast (s5cmd, no tracking):
    abc data push ./genome.fa s3://su-mbhg-hostgen/user/calm-dassie/genome.fa

  Fetch data from the internet into MinIO (server-side Nomad job):
    abc data fetch https://example.com/data.tar.gz

  Fetch a public sequence dataset by accession:
    abc data fetch SRR000001

  Download from MinIO to your local machine (resumable, aria2c):
    abc data pull s3://su-mbhg-hostgen/user/calm-dassie/results.csv

  Stage a MinIO file into your workbench ~/data/:
    abc data stage s3://su-mbhg-hostgen/user/calm-dassie/genome.fa

  Share a file with your group:
    abc data share s3://su-mbhg-hostgen/user/calm-dassie/results.vcf --to shared

  Generate a presigned link for external collaborators:
    abc data presign s3://su-mbhg-hostgen/user/calm-dassie/report.pdf --expires 48h

  Browse your MinIO bucket:
    abc data list s3://su-mbhg-hostgen/user/calm-dassie/

  Show disk usage:
    abc data disk-usage s3://su-mbhg-hostgen/user/calm-dassie/

Deletion (three tiers, increasing finality):
  delete (del)  soft delete to trash/ — reversible (abc data trash restore)
  remove (rm)   permanent, current version — with confirmation (Unix rm)
  purge         permanent, ALL versions — type 'purge' to confirm
  trash         list / restore / empty your soft-deleted objects

Plumbing commands (short aliases accepted):
  list (ls)  sync  cat  pipe  disk-usage (du)
  make-bucket (mb)  remove-bucket (rb)  stat`,
	}

	// ── Porcelain: tus upload ────────────────────────────────────────────────
	cmd.AddCommand(newUploadCmd(serverURL, accessToken, workspace, f))
	cmd.AddCommand(newUploadsCmd())
	cmd.AddCommand(newEncryptCmd())
	cmd.AddCommand(newDecryptCmd())

	// ── Porcelain: data movement ─────────────────────────────────────────────
	cmd.AddCommand(newFetchCmd(serverURL, accessToken, workspace)) // external/accession → MinIO
	cmd.AddCommand(newPushCmd())                                   // local → MinIO (s5cmd)
	cmd.AddCommand(newPullCmd())                                   // MinIO → local (s5cmd)
	cmd.AddCommand(newStageCmd())                                  // MinIO → workbench
	cmd.AddCommand(newPresignCmd())                                // generate presigned URL
	cmd.AddCommand(newShareCmd())                                  // intra-group: server-side copy → shared/ or common/
	cmd.AddCommand(newSendCmd())                                   // limited-time self-destructing outbound link (transfer.sh)

	// ── Porcelain: accession-based acquisition ───────────────────────────────
	cmd.AddCommand(newDownloadCmd(serverURL, accessToken, workspace, PipelineFactory))

	// ── Porcelain: rclone Nomad jobs (server-side, backwards-compat) ─────────
	cmd.AddCommand(newCopyCmd(serverURL, accessToken, workspace))
	cmd.AddCommand(newMoveCmd(serverURL, accessToken, workspace))

	// ── Deletion: three-tier model ─────────────────────────────────────────
	// delete → trash (reversible); remove → permanent (Unix rm); purge → all versions.
	cmd.AddCommand(newDeleteCmd()) // delete  alias: del  (soft delete to trash, reversible)
	cmd.AddCommand(newRemoveCmd()) // remove  alias: rm   (permanent, current version — Unix rm)
	cmd.AddCommand(newPurgeCmd())  // purge               (permanent, all versions)
	cmd.AddCommand(newTrashCmd())  // trash list/restore/empty

	// ── Plumbing: s5cmd / mcli wrappers ─────────────────────────────────────
	// Canonical names are full English words; unix short forms are registered aliases.
	cmd.AddCommand(newListCmd())         // list         alias: ls
	cmd.AddCommand(newSyncCmd())         // sync
	cmd.AddCommand(newCatCmd())          // cat
	cmd.AddCommand(newPipeCmd())         // pipe
	cmd.AddCommand(newDiskUsageCmd())    // disk-usage   alias: du
	cmd.AddCommand(newMakeBucketCmd())   // make-bucket  alias: mb
	cmd.AddCommand(newRemoveBucketCmd()) // remove-bucket alias: rb
	cmd.AddCommand(newStatCmd())         // stat

	return cmd
}
