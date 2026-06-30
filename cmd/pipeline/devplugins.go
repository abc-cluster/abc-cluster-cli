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

// stableNfNomadVersion / stableNfNomadS5cmdVersion are the pinned stable
// (non-prerelease) plugin versions this abc-cluster build defaults to. They are
// explicit releases — never an `-edge*` prerelease — so a fresh run is
// reproducible and never silently picks up a newly-published prerelease.
//
// nf-nomad 0.4.2+ declares `requires >=26.04.3`, so these defaults must stay in
// sync with the NfVersion defaults (pipeline + module spec) — keep the head
// Nextflow at >=26.04.3 or the pinned plugin won't load.
const (
	stableNfNomadVersion      = "0.4.4"
	stableNfNomadS5cmdVersion = "0.1.7"
)

// defaultClusterPlugins is the baseline plugin set this abc-cluster build
// assumes every Nextflow pipeline launched against this cluster needs. Merged
// into spec.Plugins after spec defaults run, only for IDs not already pinned
// by --plugin or by a saved spec — so user overrides win.
//
// These are published Nextflow plugins (resolved via Nextflow's plugin
// registry), distinct from the 99.99.99 dev bundle that flows via --dev-plugins.
//
// Both default to PINNED stable releases (stableNfNomadVersion /
// stableNfNomadS5cmdVersion) rather than a bare `id "<name>"`. A bare id makes
// Nextflow resolve to the registry's NEWEST published version, which now
// includes `-edge*` prereleases (e.g. 0.5.0-edge2) — runs must never pull a
// prerelease, so we pin explicit stable versions instead. Pinning BOTH plugins
// keeps the block all-pinned, which satisfies validatePluginVersionConsistency
// (Nextflow only auto-resolves when ALL plugins are bare OR ALL are pinned).
//
// nf-nomad 0.4.2+ requires Nextflow >=26.04.3, so the pinned nf-nomad here is
// only loadable if the head Nextflow default (spec.NfVersion) is >=26.04.3 —
// keep the two in sync. To override the nf-nomad version ad-hoc, use the general
// `--plugin id@version` flag on `run` (e.g. --plugin nf-nomad@0.4.4) — there is
// no dedicated --nf-plugin-version flag on `run`. nfNomadVersion here carries a
// saved-spec nf-nomad pin (spec.NfPluginVersion, set via `pipeline add/update`);
// empty = the pinned stable default.
//
// TODO: move to cluster-capability config (contexts.<name>.cluster.plugins)
// once a second deployment needs a different baseline. Hard-coded here keeps
// the seedling-prod baseline visible in one place for now.
//
// Precedence (highest → lowest):
//  1. --plugin id@version (ad-hoc CLI override on `run`)
//  2. saved-spec nf-nomad pin (spec.NfPluginVersion → nfNomadVersion)
//  3. defaultClusterPlugins() (this function — both pinned to stable releases)
//
// --dev-plugins replaces the entire set with defaultDevPlugins() (99.99.99
// versions), bypassing this baseline.
func defaultClusterPlugins(nfNomadVersion string) []PluginRef {
	nfNomad := strings.TrimSpace(nfNomadVersion)
	if nfNomad == "" {
		nfNomad = stableNfNomadVersion
	}
	return []PluginRef{
		{ID: "nf-nomad", Version: nfNomad},
		{ID: "nf-nomad-s5cmd", Version: stableNfNomadS5cmdVersion},
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

// validatePluginVersionConsistency rejects a plugins block that mixes
// explicitly-versioned entries with bare ones.
//
// Why: Nextflow auto-resolves a bare `id "<plugin>"` to the newest published
// release only when EVERY plugin in the block is bare. The moment any entry is
// pinned (`id "<plugin>@<version>"`), Nextflow stops auto-resolving the bare
// ones and the head fails at startup with `Unknown Nextflow plugin '<id>'`.
//
// Customizing a plugin version is fully supported — but it must be consistent:
// pin all baseline plugins, or pin none and let them all track newest. Returns
// nil for an all-bare or all-pinned set (and for the dev set, which is all
// pinned to 99.99.99).
func validatePluginVersionConsistency(plugins []PluginRef) error {
	var pinned, bare []string
	for _, p := range plugins {
		if strings.TrimSpace(p.Version) != "" {
			pinned = append(pinned, p.ID+"@"+p.Version)
		} else {
			bare = append(bare, p.ID)
		}
	}
	if len(pinned) == 0 || len(bare) == 0 {
		return nil
	}
	return fmt.Errorf(
		"mixed plugin versions: pinned [%s] but [%s] would be left unversioned.\n"+
			"Nextflow only auto-resolves bare plugin ids when EVERY plugin is bare; "+
			"once any is pinned it cannot resolve the rest and the run fails with "+
			"\"Unknown Nextflow plugin\".\n"+
			"Fix one of:\n"+
			"  • pin every plugin too — add a --plugin <id>@<version> for each bare "+
			"plugin (e.g. --plugin %s@<version>); or\n"+
			"  • pin none — drop the --plugin flags so all resolve to the newest "+
			"published release.",
		strings.Join(pinned, ", "), strings.Join(bare, ", "), bare[0])
}
