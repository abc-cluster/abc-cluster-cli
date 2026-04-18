package utils

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunNomadCLI_InjectsNomadEnvFromArgs(t *testing.T) {
	t.Setenv("NOMAD_ADDR", "http://old:4646")
	t.Setenv("NOMAD_TOKEN", "old-token")
	t.Setenv("NOMAD_REGION", "old-region")

	var out bytes.Buffer
	err := RunNomadCLI(
		context.Background(),
		[]string{"-c", `printf "%s|%s|%s" "$NOMAD_ADDR" "$NOMAD_TOKEN" "$NOMAD_REGION"`},
		"sh",
		"http://config.example:4646",
		"config-token",
		"za-cpt",
		nil,
		&out,
		&out,
	)
	if err != nil {
		t.Fatalf("RunNomadCLI: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := "http://config.example:4646|config-token|za-cpt"
	if got != want {
		t.Fatalf("unexpected NOMAD_* env: got %q want %q", got, want)
	}
}

func TestRunNomadCLI_LoadsNomadEnvFromActiveContext(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("ABC_CONFIG_FILE", cfgPath)
	raw := `version: "1"
active_context: dev
contexts:
  dev:
    endpoint: https://api.example.com
    access_token: api-token
    admin:
      services:
        nomad:
          nomad_addr: http://ctx-nomad:4646
          nomad_token: ctx-token
          nomad_region: eu-west
`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := RunNomadCLI(
		context.Background(),
		[]string{"-c", `printf "%s|%s|%s" "$NOMAD_ADDR" "$NOMAD_TOKEN" "$NOMAD_REGION"`},
		"sh",
		"",
		"",
		"",
		nil,
		&out,
		&out,
	)
	if err != nil {
		t.Fatalf("RunNomadCLI: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := "http://ctx-nomad:4646|ctx-token|eu-west"
	if got != want {
		t.Fatalf("unexpected NOMAD_* env: got %q want %q", got, want)
	}
}

func TestRunNomadCLI_InjectsNomadNamespaceFromAbcNodes(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("ABC_CONFIG_FILE", cfgPath)
	t.Setenv("NOMAD_NAMESPACE", "")
	raw := `version: "1"
active_context: lab
contexts:
  lab:
    endpoint: https://api.example.com
    access_token: api-token
    cluster_type: abc-nodes
    admin:
      services:
        nomad:
          nomad_addr: http://ctx-nomad:4646
          nomad_token: ctx-token
          nomad_region: global
      abc_nodes:
        nomad_namespace: apps
`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := RunNomadCLI(
		context.Background(),
		[]string{"-c", `printf "%s" "$NOMAD_NAMESPACE"`},
		"sh",
		"",
		"",
		"",
		nil,
		&out,
		&out,
	)
	if err != nil {
		t.Fatalf("RunNomadCLI: %v", err)
	}
	if strings.TrimSpace(out.String()) != "apps" {
		t.Fatalf("NOMAD_NAMESPACE: got %q want apps", strings.TrimSpace(out.String()))
	}
}
