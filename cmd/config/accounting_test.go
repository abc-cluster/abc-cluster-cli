package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
)

// withConfigDir points ABC_CONFIG_FILE at a temp config so the round-trip
// tests don't touch the user's real ~/.abc/config.yaml. Returns a context
// name pre-seeded into the file.
func withConfigDir(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const seed = `version: "1.0"
active_context: testctx
contexts:
  testctx:
    endpoint: https://example.test
`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	t.Setenv("ABC_CONFIG_FILE", path)
	return path, "testctx"
}

func TestAccountingSetShowUnsetRoundTrip(t *testing.T) {
	cfgPath, ctxName := withConfigDir(t)
	_ = ctxName

	// Set a few keys.
	cmd := newAccountingCmd()
	cmd.SetArgs([]string{"set", "cost.cpu_hour=0.45", "currency=ZAR"})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Read back the YAML.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	if !strings.Contains(yaml, "accounting:") || !strings.Contains(yaml, "cpu_hour") {
		t.Errorf("config file missing accounting block:\n%s", yaml)
	}

	// LoadLayer1 sees the values.
	l1, err := acct.LoadLayer1("testctx")
	if err != nil {
		t.Fatal(err)
	}
	if l1.Accounting[acct.KeyCostCpuHour] != "0.45" {
		t.Errorf("loaded cpu_hour = %q, want 0.45", l1.Accounting[acct.KeyCostCpuHour])
	}
	if l1.Accounting[acct.KeyCurrency] != "ZAR" {
		t.Errorf("loaded currency = %q, want ZAR", l1.Accounting[acct.KeyCurrency])
	}

	// Unset cpu_hour.
	cmd2 := newAccountingCmd()
	cmd2.SetArgs([]string{"unset", "cost.cpu_hour"})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetOut(&bytes.Buffer{})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("unset: %v", err)
	}
	l2, _ := acct.LoadLayer1("testctx")
	if _, has := l2.Accounting[acct.KeyCostCpuHour]; has {
		t.Errorf("cpu_hour still present after unset: %v", l2.Accounting)
	}
	if l2.Accounting[acct.KeyCurrency] != "ZAR" {
		t.Errorf("currency lost after unrelated unset: %v", l2.Accounting)
	}
}

func TestAccountingSetRejectsInvalidValues(t *testing.T) {
	withConfigDir(t)

	cases := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"set", "cost.cpu_hour=-0.1"}, "must be ≥ 0"},
		{[]string{"set", "currency=zar"}, "ISO-4217"},
		{[]string{"set", "bogus=1"}, "unknown accounting key"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			cmd := newAccountingCmd()
			cmd.SetArgs(c.args)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q does not mention %q", err.Error(), c.wantErr)
			}
		})
	}
}

func TestAccountingShowSurfacesSourceTags(t *testing.T) {
	withConfigDir(t)

	// Set just one key to have at least one config-source line.
	setCmd := newAccountingCmd()
	setCmd.SetArgs([]string{"set", "cost.cpu_hour=0.45"})
	setCmd.SetErr(&bytes.Buffer{})
	setCmd.SetOut(&bytes.Buffer{})
	if err := setCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	showCmd := newAccountingCmd()
	showCmd.SetArgs([]string{"show"})
	showCmd.SetOut(&stdout)
	showCmd.SetErr(&bytes.Buffer{})
	if err := showCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"cost.cpu_hour", "config", "cost.gpu_hour", "built-in", "currency"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q\n%s", want, out)
		}
	}
}
