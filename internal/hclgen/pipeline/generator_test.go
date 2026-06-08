package pipeline

import (
	"strings"
	"testing"
)

func TestGenerate_StaticEnvAndMonitoringMeta(t *testing.T) {
	spec := Spec{
		Datacenters:     []string{"dc1"},
		WorkDir:         "/work/nextflow-work",
		CPU:             1000,
		MemoryMB:        2048,
		NfVersion:       "25.10.4",
		NfPluginVersion: "0.4.0-edge3",
		Repository:      "nextflow-io/hello",
		StaticEnv: map[string]string{
			"ABC_NODES_PROMETHEUS_REMOTE_WRITE_URL": "http://10.0.0.1:9090/api/v1/write",
		},
	}
	hcl := Generate(spec, "http://127.0.0.1:4646", "tok", "run-uuid-test")
	if !strings.Contains(hcl, `abc_monitoring_floor`) || !strings.Contains(hcl, `enhanced`) {
		t.Fatalf("expected job meta abc_monitoring_floor when StaticEnv set:\n%s", hcl)
	}
	if !strings.Contains(hcl, `ABC_NODES_PROMETHEUS_REMOTE_WRITE_URL`) {
		t.Fatalf("expected static env in task env:\n%s", hcl)
	}
	if !strings.Contains(hcl, `run_uuid`) || !strings.Contains(hcl, `run-uuid-test`) {
		t.Fatalf("expected run_uuid meta:\n%s", hcl)
	}
}

func TestGenerate_NoStaticEnv_NoMonitoringMeta(t *testing.T) {
	spec := Spec{
		Datacenters:     []string{"dc1"},
		WorkDir:         "/work/nextflow-work",
		CPU:             1000,
		MemoryMB:        2048,
		NfVersion:       "25.10.4",
		NfPluginVersion: "0.4.0-edge3",
		Repository:      "nextflow-io/hello",
	}
	hcl := Generate(spec, "http://127.0.0.1:4646", "tok", "uuid-2")
	if strings.Contains(hcl, `abc_monitoring_floor`) {
		t.Fatalf("did not expect abc_monitoring_floor without StaticEnv:\n%s", hcl)
	}
}

// TestGenerate_PluginsBlock_VersionForms locks the plugins{} rendering:
//   - empty NfPluginVersion + no Plugins → bare `id "nf-nomad"` (Nextflow
//     resolves to newest; a literal `@latest` is rejected by the plugin index).
//   - empty-version PluginRef → bare `id "<name>"`.
//   - pinned version → `id "<name>@<version>"`.
// The bare-id path was previously untested, which let an `@latest` regression
// (rejected live with "Unknown plugin id: nf-nomad") ship undetected.
func TestGenerate_PluginsBlock_VersionForms(t *testing.T) {
	t.Run("empty NfPluginVersion, no Plugins → bare id nf-nomad", func(t *testing.T) {
		spec := Spec{
			Datacenters: []string{"dc1"},
			WorkDir:     "/work/nextflow-work",
			CPU:         1000,
			MemoryMB:    2048,
			NfVersion:   "26.04.2",
			Repository:  "nextflow-io/hello",
		}
		hcl := Generate(spec, "http://127.0.0.1:4646", "tok", "uuid-bare")
		if !strings.Contains(hcl, `id "nf-nomad"`) {
			t.Fatalf("expected bare `id \"nf-nomad\"` for empty NfPluginVersion:\n%s", hcl)
		}
		if strings.Contains(hcl, `id "nf-nomad@"`) || strings.Contains(hcl, `nf-nomad@latest`) {
			t.Fatalf("must not emit `nf-nomad@` / `@latest` when version is empty:\n%s", hcl)
		}
	})

	t.Run("empty-version PluginRefs → bare ids", func(t *testing.T) {
		spec := Spec{
			Datacenters: []string{"dc1"},
			WorkDir:     "/work/nextflow-work",
			CPU:         1000,
			MemoryMB:    2048,
			NfVersion:   "26.04.2",
			Repository:  "nextflow-io/hello",
			Plugins: []PluginRef{
				{ID: "nf-nomad", Version: ""},
				{ID: "nf-nomad-s5cmd", Version: ""},
			},
		}
		hcl := Generate(spec, "http://127.0.0.1:4646", "tok", "uuid-bare2")
		if !strings.Contains(hcl, `id "nf-nomad"`) || !strings.Contains(hcl, `id "nf-nomad-s5cmd"`) {
			t.Fatalf("expected bare ids for empty-version PluginRefs:\n%s", hcl)
		}
		if strings.Contains(hcl, `@latest`) || strings.Contains(hcl, `id "nf-nomad-s5cmd@"`) {
			t.Fatalf("empty-version PluginRef must render without `@`:\n%s", hcl)
		}
	})

	t.Run("pinned version → id name@version", func(t *testing.T) {
		spec := Spec{
			Datacenters: []string{"dc1"},
			WorkDir:     "/work/nextflow-work",
			CPU:         1000,
			MemoryMB:    2048,
			NfVersion:   "26.04.2",
			Repository:  "nextflow-io/hello",
			Plugins: []PluginRef{
				{ID: "nf-nomad", Version: "0.4.0-edge8"},
				{ID: "nf-nomad-s5cmd", Version: "0.1.2"},
			},
		}
		hcl := Generate(spec, "http://127.0.0.1:4646", "tok", "uuid-pin")
		if !strings.Contains(hcl, `id "nf-nomad@0.4.0-edge8"`) || !strings.Contains(hcl, `id "nf-nomad-s5cmd@0.1.2"`) {
			t.Fatalf("expected pinned `id name@version`:\n%s", hcl)
		}
	})
}

// TestGenerate_S5cmdBlock_SkipTLS checks that:
//   - When an S3 work dir + nf-nomad-s5cmd plugin + S5cmdSkipTLS=true are
//     combined, the generated nextflow config contains `useTLS = false`.
//   - When S5cmdSkipTLS=false the line is absent (public-CA deployments don't
//     need it and should not emit it).
//   - The nf-work host volume is mounted at /nxf-work (so s5cmd is found at
//     /nxf-work/bin/s5cmd inside the worker container).
func TestGenerate_S5cmdBlock_SkipTLS(t *testing.T) {
	base := Spec{
		Datacenters:     []string{"dc1"},
		WorkDir:         "s3://su-mbhg-hostgen/work",
		CPU:             1000,
		MemoryMB:        2048,
		NfVersion:       "25.10.4",
		NfPluginVersion: "99.99.99",
		Repository:      "nextflow-io/hello",
		Plugins: []PluginRef{
			{ID: "nf-nomad", Version: "99.99.99"},
			{ID: "nf-nomad-s5cmd", Version: "99.99.99"},
		},
	}

	t.Run("S5cmdSkipTLS=true emits useTLS=false", func(t *testing.T) {
		spec := base
		spec.S5cmdSkipTLS = true
		cfg := buildNextflowConfig(spec)
		if !strings.Contains(cfg, "useTLS") || !strings.Contains(cfg, "false") {
			t.Fatalf("expected useTLS = false in s5cmd block:\n%s", cfg)
		}
	})

	t.Run("S5cmdSkipTLS=false omits useTLS line", func(t *testing.T) {
		spec := base
		spec.S5cmdSkipTLS = false
		cfg := buildNextflowConfig(spec)
		if strings.Contains(cfg, "useTLS") {
			t.Fatalf("did not expect useTLS when S5cmdSkipTLS=false:\n%s", cfg)
		}
	})

	t.Run("abc-tools volume mounted at /nxf-work", func(t *testing.T) {
		spec := base
		cfg := buildNextflowConfig(spec)
		// ADR-0061: tools volume is `abc-tools` (bin/ layout) mounted at /nxf-work so
		// the s5cmd plugin bootstrap still finds /nxf-work/bin/s5cmd.
		if !strings.Contains(cfg, `name: "abc-tools"`) || !strings.Contains(cfg, `path: "/nxf-work"`) {
			t.Fatalf("expected abc-tools volume at /nxf-work:\n%s", cfg)
		}
	})

	t.Run("s5cmd bucket and prefix extracted from work dir", func(t *testing.T) {
		spec := base
		cfg := buildNextflowConfig(spec)
		if !strings.Contains(cfg, `"s3://su-mbhg-hostgen"`) {
			t.Fatalf("expected bucket s3://su-mbhg-hostgen:\n%s", cfg)
		}
		if !strings.Contains(cfg, `"work"`) {
			t.Fatalf("expected prefix 'work':\n%s", cfg)
		}
	})
}
