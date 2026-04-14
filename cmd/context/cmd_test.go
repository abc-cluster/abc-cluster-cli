package contextcmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

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
		"--cluster", "dev-cluster",
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
