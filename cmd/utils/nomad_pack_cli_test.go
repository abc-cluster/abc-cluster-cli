package utils

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunNomadPackCLI_InjectsNomadEnvFromActiveContext(t *testing.T) {
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
      abc_nodes:
        nomad_namespace: apps
`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := RunNomadPackCLI(
		context.Background(),
		[]string{"-c", `printf "%s|%s|%s|%s" "$NOMAD_ADDR" "$NOMAD_TOKEN" "$NOMAD_REGION" "$NOMAD_NAMESPACE"`},
		"sh",
		nil,
		&out,
		&out,
	)
	if err != nil {
		t.Fatalf("RunNomadPackCLI: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := "http://ctx-nomad:4646|ctx-token|eu-west|apps"
	if got != want {
		t.Fatalf("unexpected env: got %q want %q", got, want)
	}
}