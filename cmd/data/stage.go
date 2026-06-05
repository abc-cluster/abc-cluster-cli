package data

// stage.go — `abc data stage` — move data WITHIN the cluster between storage
// locations. Dispatches a Nomad job; the user's local machine is not involved.
//
// Supported moves:
//   s3:// → workbench   copy a MinIO object into the pool slot's ~/data/ dir
//   (s3:// → s3:// is planned but not yet implemented)
//
// This is the command to use when:
//   "I uploaded a file to MinIO and want to see it in my JupyterLab session."
//   "My pipeline wrote results to MinIO and I want to browse them in the notebook."

import (
	"fmt"
	"strings"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newStageCmd() *cobra.Command {
	var to string
	var node string
	var name string

	cmd := &cobra.Command{
		Use:   "stage <s3-uri>",
		Short: "Copy a stored object into the workbench home or another cluster location",
		Long: `Copy data between cluster storage locations without downloading to your machine.

The most common use: copy a stored object into your workbench (JupyterLab) home
directory so you can browse or process it interactively.

A Nomad job is submitted and runs on the platform node (aither). The copy uses
s5cmd with checksum verification. File ownership is set to your JupyterLab slot
user so the notebook can read and write the file immediately.

The workbench home directory layout after staging:

  ~/data/<filename>          ← file appears here in JupyterLab

Examples:

  # Stage a file from storage into your JupyterLab session:
  abc data stage s3://su-mbhg-hostgen/user/calm-dassie/data/genome.fa

  # Stage an entire prefix (trailing / = all objects under the prefix):
  abc data stage s3://su-mbhg-hostgen/user/calm-dassie/results/

  # Override the platform node (for non-standard deployments):
  abc data stage s3://su-mbhg-hostgen/user/calm-dassie/data/genome.fa \
    --node grove-platform01

Requires --to workbench (default) — S3-to-S3 staging will be added in a future release.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := strings.TrimSpace(args[0])
			if !strings.HasPrefix(strings.ToLower(src), "s3://") {
				return fmt.Errorf("source must be an S3 URI (s3://...); got %q\n"+
					"  To fetch from the internet, use: abc data fetch <url>", src)
			}

			to = strings.ToLower(strings.TrimSpace(to))
			switch to {
			case "workbench", "":
				return runStageToWorkbench(cmd, src, node, name)
			default:
				if strings.HasPrefix(to, "s3://") {
					return fmt.Errorf("S3-to-S3 staging is not yet implemented")
				}
				return fmt.Errorf("unsupported --to value %q; only 'workbench' is supported", to)
			}
		},
	}

	cmd.Flags().StringVar(&to, "to", "workbench",
		`staging destination:
  workbench   copy into the active pool slot's ~/data/ directory in JupyterLab (default)`)
	cmd.Flags().StringVar(&node, "node", "",
		"override the platform node name (default: aither; reads workbench.node from config)")
	cmd.Flags().StringVar(&name, "name", "",
		"custom Nomad job name (default: data-workbench-stage)")

	return cmd
}

// runStageToWorkbench copies src (S3 URI) to the workbench home of the active
// pool slot. nodeOverride pins the Nomad job to a specific node; when empty
// the node is read from config (defaults to "aither").
func runStageToWorkbench(cmd *cobra.Command, src, nodeOverride, jobName string) error {
	if nodeOverride == "" {
		// Normal path — StageS3ToWorkbench resolves the node from config.
		return StageS3ToWorkbench(cmd, src)
	}

	// Node-override path: replicate StageS3ToWorkbench with the explicit node.
	cfg, err := abccfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()

	slot, err := workbenchSlotFromCtx(actx)
	if err != nil {
		return err
	}

	endpoint, accessKey, secretKey, err := resolveWorkbenchMinioCredsFromCtx(actx)
	if err != nil {
		return err
	}

	dstDir := fmt.Sprintf("/data/workbench/%s/home/data", slot)

	var script string
	if strings.HasSuffix(src, "/") {
		script = buildWorkbenchStageScriptMulti(endpoint, accessKey, secretKey, src, dstDir, slot)
	} else {
		filename := stagePathBase(src)
		dst := dstDir + "/" + filename
		script = buildWorkbenchStageScript(endpoint, accessKey, secretKey, src, dst, slot)
	}

	runName := strings.TrimSpace(jobName)
	if runName == "" {
		runName = "data-workbench-stage"
	}

	nomadOpts := dataNomadScriptOpts{
		RunName: runName,
		Driver:  "raw_exec",
		ExtraConstraints: []string{
			fmt.Sprintf("node.unique.name==%s", nodeOverride),
		},
	}
	if artURL := toolNomadArtifactURL("s5cmd"); artURL != "" {
		nomadOpts.Artifacts = []downloadArtifact{{URL: artURL, Dest: "local/"}}
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Staging to workbench slot %q → %s/ (node: %s)...\n", slot, dstDir, nodeOverride)
	return submitDataNomadScript(cmd, nomadOpts, script)
}

// stagePathBase returns the last "/" -separated segment of an S3 URI or path.
func stagePathBase(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
