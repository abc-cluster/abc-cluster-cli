package pipeline

import (
	"strings"
	"testing"
)

func tierSpec() Spec {
	return Spec{
		Datacenters: []string{"dc1"}, WorkDir: "s3://bucket/wd",
		CPU: 1000, MemoryMB: 2048, NfVersion: "26.04.3",
		NfPluginVersion: "0.5.0-edge6", Repository: "nf-core/demo",
		Namespace: "default",
	}
}

// Everything abc injected used to arrive via -c, which outranks the
// repository's nextflow.config — so a pipeline setting executor.queueSize lost
// silently. Values that describe the SHAPE of the run belong in the defaults
// tier, which Nextflow reads below the pipeline.
func TestConfigTiers_WorkloadShapeIsOverridable(t *testing.T) {
	spec := tierSpec()
	enforced := buildNextflowConfig(spec)
	defaults := buildOverridableDefaults(spec)

	// These are the pipeline author's call.
	for _, key := range []string{"queueSize", "cpuMode", "failOnPlacementFailure", "maxRetries", "errorStrategy", "deleteOnCompletion"} {
		if strings.Contains(enforced, key) && !strings.Contains(enforced, "// "+key) {
			t.Errorf("%q must not be enforced — a pipeline that sets it would lose silently", key)
		}
		if !strings.Contains(defaults, key) {
			t.Errorf("%q missing from the overridable defaults", key)
		}
	}
}

// The platform's contract with the cluster: identity, endpoint, tenancy, data
// layout, and the shared Nomad server's connection budget.
func TestConfigTiers_PlatformInvariantsStayEnforced(t *testing.T) {
	enforced := buildNextflowConfig(tierSpec())
	for _, want := range []string{
		`executor      = "nomad"`, // the point of the platform
		`workDir = "s3://bucket/wd"`,
		`namespace                = "default"`,
		`privileged               = false`,
		`submitRateLimit = "10/1sec"`, // protects co-located tenants
		`id "nf-nomad@0.5.0-edge6"`,
	} {
		if !strings.Contains(enforced, want) {
			t.Errorf("enforced config missing %q:\n%s", want, enforced)
		}
	}
}

// failOnPlacementFailure=true fails tasks that are merely queued behind a
// saturated node — on a single-node cluster, the normal case. abc forced it on
// while the plugin's own default is false, and the pipeline could not turn it
// off. It must now default off and be overridable.
func TestConfigTiers_PlacementFailureDefaultsOff(t *testing.T) {
	defaults := buildOverridableDefaults(tierSpec())
	if !strings.Contains(defaults, "failOnPlacementFailure  = false") {
		t.Errorf("failOnPlacementFailure must default to the plugin's own false:\n%s", defaults)
	}
	if strings.Contains(buildNextflowConfig(tierSpec()), "failOnPlacementFailure   = true") {
		t.Error("failOnPlacementFailure must no longer be enforced true")
	}
}

// abc's executor plugins are how the run reaches the cluster; a pipeline
// replacing them has no executor. Everything else is the pipeline's business —
// nf-core/viralrecon pins nf-schema@2.5.1 and must be able to keep it.
func TestConfigTiers_PluginOwnershipSplit(t *testing.T) {
	spec := tierSpec()
	spec.Plugins = []PluginRef{
		{ID: "nf-schema", Version: "2.5.1"},
		{ID: "nf-nomad-s5cmd", Version: "0.1.7"},
	}
	enforced := buildNextflowConfig(spec)
	defaults := buildOverridableDefaults(spec)

	if !strings.Contains(enforced, `id "nf-nomad@0.5.0-edge6"`) {
		t.Errorf("nf-nomad must stay enforced:\n%s", enforced)
	}
	if !strings.Contains(enforced, `id "nf-nomad-s5cmd@0.1.7"`) {
		t.Errorf("nf-nomad-s5cmd must stay enforced:\n%s", enforced)
	}
	if strings.Contains(enforced, "nf-schema") {
		t.Errorf("a pipeline plugin must not be enforced:\n%s", enforced)
	}
	if !strings.Contains(defaults, `id "nf-schema@2.5.1"`) {
		t.Errorf("a pipeline plugin belongs in the overridable tier:\n%s", defaults)
	}
}

// Regression: a non-empty Plugins list used to REPLACE the whole block, so
// supplying any plugin silently dropped nf-nomad and left the run with no
// executor.
func TestConfigTiers_PipelinePluginsDoNotDropNfNomad(t *testing.T) {
	spec := tierSpec()
	spec.Plugins = []PluginRef{{ID: "nf-schema", Version: "2.5.1"}}
	if !strings.Contains(buildNextflowConfig(spec), `id "nf-nomad@`) {
		t.Error("supplying a pipeline plugin must not drop nf-nomad")
	}
}

// A later -c wins, so ordering is the whole mechanism: the user's --config
// must beat the repository and abc's defaults, and the enforced file must beat
// everything.
func TestConfigTiers_EnforcedConfigIsTheFinalDashC(t *testing.T) {
	spec := tierSpec()
	spec.ExtraConfig = "executor.queueSize = 3\n"
	hcl := Generate(spec, "http://127.0.0.1:4646", "tok", "u")

	iUser := strings.Index(hcl, "-c /local/nextflow.user.config")
	iEnf := strings.Index(hcl, "-c /local/nextflow.headjob.config")
	if iUser < 0 {
		t.Fatalf("user --config must be passed as its own -c:\n%s", hcl)
	}
	if iEnf < iUser {
		t.Error("the enforced config must be the LAST -c, after the user's")
	}
	// And it must no longer be concatenated into the enforced file, where it
	// would override the values abc has to guarantee.
	if strings.Contains(buildNextflowConfig(spec), "queueSize = 3") {
		t.Error("user --config must not be appended into the enforced config")
	}
}

// The defaults only outrank nothing-at-all unless Nextflow actually reads them,
// which means landing at $NXF_HOME/config before nextflow runs.
func TestConfigTiers_DefaultsInstalledIntoNxfHome(t *testing.T) {
	hcl := Generate(tierSpec(), "http://127.0.0.1:4646", "tok", "u")
	if !strings.Contains(hcl, "local/nextflow.defaults.config") {
		t.Error("defaults template not emitted")
	}
	if !strings.Contains(hcl, "cp /local/nextflow.defaults.config /local/.nxf-home/config") {
		t.Errorf("entrypoint must install the defaults at $NXF_HOME/config:\n%s", hcl)
	}
}
