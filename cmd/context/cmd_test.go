package contextcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"gopkg.in/yaml.v3"
	"github.com/spf13/cobra"
)

func executeContextCmd(cmd *cobra.Command, args ...string) (string, error) {
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	_, err := cmd.ExecuteC()
	return buf.String(), err
}

func TestContextAddAndUse(t *testing.T) {
	tmpConfig := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("ABC_CONFIG_FILE", tmpConfig)

	cmd := NewCmd()
	out, err := executeContextCmd(cmd,
		"add", "org-a-za-cpt",
		"--endpoint", "https://api.abc-cluster.io",
		"--upload-endpoint", "https://uploads.abc-cluster.io/files/",
		"--access-token", "token-value",
		"--organization-id", "org-dev",
		"--workspace-id", "ws-org-a-01",
		"--region", "za-cpt",
	)
	if err != nil {
		t.Fatalf("unexpected error adding context: %v", err)
	}
	if !strings.Contains(out, "Added and activated context \"org-a-za-cpt\"") {
		t.Fatalf("unexpected output: %q", out)
	}

	cmd = NewCmd()
	out, err = executeContextCmd(cmd, "list")
	if err != nil {
		t.Fatalf("unexpected error listing contexts: %v", err)
	}
	if !strings.Contains(out, "org-a-za-cpt") {
		t.Fatalf("context name missing from list: %q", out)
	}
	if !strings.Contains(out, "*") {
		t.Fatalf("active context marker missing from list: %q", out)
	}

	cmd = NewCmd()
	out, err = executeContextCmd(cmd, "use", "org-a-za-cpt")
	if err != nil {
		t.Fatalf("unexpected error switching context: %v", err)
	}
	if !strings.Contains(out, "Switched active context to org-a-za-cpt") {
		t.Fatalf("unexpected output on use: %q", out)
	}
}

func TestContextAddDerivesUploadEndpointWhenOmitted(t *testing.T) {
	tmpConfig := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("ABC_CONFIG_FILE", tmpConfig)

	cmd := NewCmd()
	_, err := executeContextCmd(cmd, "add", "dev", "--endpoint", "https://api.example.com/v1", "--access-token", "tok")
	if err != nil {
		t.Fatalf("add context: %v", err)
	}
	raw, err := os.ReadFile(tmpConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "https://api.example.com/v1/files/") {
		t.Fatalf("expected derived upload_endpoint in config, got:\n%s", raw)
	}
}

func TestContextShowYAML_PrintsShareableDocument(t *testing.T) {
	tmpConfig := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("ABC_CONFIG_FILE", tmpConfig)

	cmd := NewCmd()
	_, err := executeContextCmd(cmd,
		"add", "dev",
		"--endpoint", "https://api.example.com",
		"--upload-token", "upload-token-123",
		"--access-token", "access-token-123",
		"--organization-id", "org-1",
		"--workspace-id", "ws-1",
		"--region", "za-cpt",
	)
	if err != nil {
		t.Fatalf("unexpected error adding context: %v", err)
	}

	cmd = NewCmd()
	out, err := executeContextCmd(cmd, "show", "yaml")
	if err != nil {
		t.Fatalf("unexpected error showing yaml: %v", err)
	}

	var payload struct {
		Version       string `yaml:"version"`
		ActiveContext string `yaml:"active_context"`
		Contexts      map[string]struct {
			Endpoint    string `yaml:"endpoint"`
			UploadToken string `yaml:"upload_token"`
			AccessToken string `yaml:"access_token"`
		} `yaml:"contexts"`
	}
	if err := yaml.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("expected valid yaml, got error: %v\n%s", err, out)
	}
	if payload.ActiveContext != "dev" {
		t.Fatalf("expected active_context dev, got %q", payload.ActiveContext)
	}
	if payload.Contexts["dev"].Endpoint != "https://api.example.com" {
		t.Fatalf("expected endpoint in yaml, got %q", payload.Contexts["dev"].Endpoint)
	}
	if payload.Contexts["dev"].UploadToken != "upload-token-123" {
		t.Fatalf("expected upload token in yaml, got %q", payload.Contexts["dev"].UploadToken)
	}
	if payload.Contexts["dev"].AccessToken != "access-token-123" {
		t.Fatalf("expected access token in yaml, got %q", payload.Contexts["dev"].AccessToken)
	}

	exportPath := filepath.Join(t.TempDir(), "exported-config.yaml")
	if err := os.WriteFile(exportPath, []byte(out), 0o600); err != nil {
		t.Fatalf("write exported yaml: %v", err)
	}
	loaded, err := cfg.LoadFrom(exportPath)
	if err != nil {
		t.Fatalf("exported yaml should be loadable as config: %v\n%s", err, out)
	}
	if loaded.ActiveContext != "dev" {
		t.Fatalf("expected active context dev in exported yaml, got %q", loaded.ActiveContext)
	}
	if got, ok := loaded.Get("contexts.dev.endpoint"); !ok || got != "https://api.example.com" {
		t.Fatalf("expected contexts.dev.endpoint in exported yaml, got (%q, %v)", got, ok)
	}
}

func TestContextShow_PrintsUnmaskedTokens(t *testing.T) {
	tmpConfig := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("ABC_CONFIG_FILE", tmpConfig)

	cmd := NewCmd()
	_, err := executeContextCmd(cmd,
		"add", "dev",
		"--endpoint", "https://api.example.com",
		"--upload-token", "upload-token-plain",
		"--access-token", "access-token-plain",
	)
	if err != nil {
		t.Fatalf("unexpected error adding context: %v", err)
	}

	cmd = NewCmd()
	out, err := executeContextCmd(cmd, "show")
	if err != nil {
		t.Fatalf("unexpected error showing context: %v", err)
	}
	if !strings.Contains(out, "Upload token: upload-token-plain") {
		t.Fatalf("expected unmasked upload token in output, got: %q", out)
	}
	if !strings.Contains(out, "Access token: access-token-plain") {
		t.Fatalf("expected unmasked access token in output, got: %q", out)
	}
}

func TestContextShowYAML_IncludesDefaults(t *testing.T) {
	tmpConfig := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("ABC_CONFIG_FILE", tmpConfig)

	cmd := NewCmd()
	_, err := executeContextCmd(cmd, "add", "dev", "--endpoint", "https://api.example.com", "--access-token", "tok")
	if err != nil {
		t.Fatalf("unexpected error adding context: %v", err)
	}

	raw := []byte("version: \"1.0\"\nactive_context: dev\ncontexts:\n  dev:\n    endpoint: https://api.example.com\n    upload_endpoint: https://api.example.com/files/\n    access_token: tok\ndefaults:\n  output: yaml\n")
	if writeErr := os.WriteFile(tmpConfig, raw, 0o644); writeErr != nil {
		t.Fatalf("write config: %v", writeErr)
	}

	cmd = NewCmd()
	out, err := executeContextCmd(cmd, "show", "yaml")
	if err != nil {
		t.Fatalf("unexpected error showing yaml: %v", err)
	}
	if !strings.Contains(out, "defaults:") || !strings.Contains(out, "output: yaml") {
		t.Fatalf("expected defaults in yaml output, got:\n%s", out)
	}
}
