package pipeline

import (
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/tools"
)

// devNextflowEntry is the [local.<name>] key in tools.toml that the
// --dev-nextflow flow expects. Operators build the custom Nextflow zip via
// `make bundle-nextflow` in the meta-plugin repo, register it under this name,
// and ship it to the cluster via `abc admin tools push`.
const devNextflowEntry = "nextflow-dev"

// devPluginBundleEntry is the [local.<name>] key in tools.toml that the
// `--dev-plugins` flow expects. Operators register the meta-plugin zip under
// this name and ship it to the cluster via `abc admin tools push`.
const devPluginBundleEntry = "nf-abc-cluster-dev"

// defaultDevPlugins is the plugin id/version set emitted in the head config's
// plugins { ... } block when --dev-plugins is requested.
//
// Note: nf-abc-cluster-dev itself is NOT a Nextflow plugin — it's a build-time
// aggregator that produces this bundle. Only real Nextflow plugins (those with
// a MANIFEST.MF) belong in the plugins { ... } block; otherwise Nextflow
// rejects the run with "Plugin '<name>' installation looks corrupted".
//
// Versions match the dev install marker (99.99.99) used by the meta-plugin's
// `make bundle` target — keep in sync with that Makefile.
func defaultDevPlugins() []PluginRef {
	return []PluginRef{
		{ID: "nf-nomad", Version: "99.99.99"},
		{ID: "nf-rclone", Version: "99.99.99"},
		{ID: "nf-nomad-s5cmd", Version: "99.99.99"},
	}
}

// defaultDevPluginBinaries names the cluster tool binaries the dev plugin set
// requires at runtime. Each entry must be registered in tools.toml so its
// per-arch URL can be resolved via tools.ArtifactURL(name, "").
//
//   - `rclone` — needed by nf-rclone (head + every compute node).
//   - `s5cmd`  — needed by nf-nomad-s5cmd's S5cmdNomadInterop, which shells out to
//                s5cmd cp from the head (to upload .command.* + sync results
//                back) and from every worker bootstrap script. Must be on the
//                head image's PATH OR available via /nxf-work/bin/ (which the
//                worker bootstrap script also probes).
//
// The head pull is automatic via the artifact stanza; child-task coverage is
// the plugin's own responsibility (see e.g. abc-place-s5cmd sysbatch which
// drops s5cmd onto every Nomad client's host volume).
func defaultDevPluginBinaries() []string {
	return []string{"rclone", "s5cmd"}
}

// resolveDevNextflow returns the cluster-side artifact URL for the custom
// Nextflow fork zip, or an actionable error if the operator hasn't registered /
// pushed it yet.
func resolveDevNextflow() (string, error) {
	url, err := tools.ArtifactURL(devNextflowEntry, "")
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "not found in tools.toml"):
			return "", fmt.Errorf(
				"--dev-nextflow: no [local.%s] entry in tools.toml.\n"+
					"  Build the custom Nextflow zip with: make bundle-nextflow  (in the meta-plugin repo)\n"+
					"  Register it with:                  abc admin tools edit\n"+
					"  Then ship it to the cluster with:  abc admin tools push",
				devNextflowEntry,
			)
		case strings.Contains(msg, "no endpoint configured"):
			return "", fmt.Errorf(
				"--dev-nextflow: no admin.tools.endpoint set on the active context.\n"+
					"  Run `abc admin tools push` first — it writes the endpoint back to config.yaml")
		default:
			return "", fmt.Errorf("--dev-nextflow: resolving Nextflow URL: %w", err)
		}
	}
	return url, nil
}

// resolveDevPluginBundle returns the cluster-side artifact URL for the dev
// plugin bundle, or an actionable error if the operator hasn't registered /
// pushed it yet.
func resolveDevPluginBundle() (string, error) {
	url, err := tools.ArtifactURL(devPluginBundleEntry, "")
	if err != nil {
		// Wrap with a hint that doesn't leak which plugins ship in the bundle.
		msg := err.Error()
		switch {
		case strings.Contains(msg, "not found in tools.toml"):
			return "", fmt.Errorf(
				"--dev-plugins: no [local.%s] entry in tools.toml.\n"+
					"  Register the meta-plugin bundle with: abc admin tools edit\n"+
					"  Then ship it to the cluster with:    abc admin tools push",
				devPluginBundleEntry,
			)
		case strings.Contains(msg, "no endpoint configured"):
			return "", fmt.Errorf(
				"--dev-plugins: no admin.tools.endpoint set on the active context.\n"+
					"  Run `abc admin tools push` first — it writes the endpoint back to config.yaml")
		default:
			return "", fmt.Errorf("--dev-plugins: resolving bundle URL: %w", err)
		}
	}
	return url, nil
}
