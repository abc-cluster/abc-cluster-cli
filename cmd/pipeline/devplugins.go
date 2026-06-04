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

// defaultClusterPlugins is the baseline plugin set this abc-cluster build
// assumes every Nextflow pipeline launched against this cluster needs. Merged
// into spec.Plugins after spec defaults run, only for IDs not already pinned
// by --plugin or by a saved spec — so user overrides win.
//
// These are published Nextflow plugins (resolved via Nextflow's plugin
// registry), distinct from the 99.99.99 dev bundle that flows via --dev-plugins.
//
// Both default to the newest published release. We express "newest" as the
// bare `id "<name>"` form (empty Version) — NOT the literal `@latest` token.
// Nextflow's plugin index resolves a version-less id to the highest available
// version, but rejects a literal `@latest` with "Unknown plugin id: nf-nomad"
// (verified live against seedling-prod, Nextflow 26.04.2). So a fresh run picks
// up the newest nf-nomad / nf-nomad-s5cmd without the CLI chasing version
// numbers, and without tripping the index. To pin a version, use the general
// `--plugin id@version` flag on `run` (e.g. --plugin nf-nomad@0.4.0-edge8) —
// there is no dedicated --nf-plugin-version flag on `run`. nfNomadVersion here
// carries a saved-spec nf-nomad pin (spec.NfPluginVersion, set via
// `pipeline add/update`); empty = newest.
//
// TODO: move to cluster-capability config (contexts.<name>.cluster.plugins)
// once a second deployment needs a different baseline. Hard-coded here keeps
// the seedling-prod baseline visible in one place for now.
//
// Precedence (highest → lowest):
//  1. --plugin id@version (ad-hoc CLI override on `run`)
//  2. saved-spec nf-nomad pin (spec.NfPluginVersion → nfNomadVersion)
//  3. defaultClusterPlugins() (this function — both bare-id = newest)
//
// --dev-plugins replaces the entire set with defaultDevPlugins() (99.99.99
// versions), bypassing this baseline.
func defaultClusterPlugins(nfNomadVersion string) []PluginRef {
	return []PluginRef{
		{ID: "nf-nomad", Version: strings.TrimSpace(nfNomadVersion)},
		{ID: "nf-nomad-s5cmd", Version: ""},
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
