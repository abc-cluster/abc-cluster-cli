package config

import (
	"bytes"
	"strings"
	"testing"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
)

func TestEmissionsSetShowUnsetRoundTrip(t *testing.T) {
	withConfigDir(t)

	cmd := newEmissionsCmd()
	cmd.SetArgs([]string{"set", "pue=1.27", "grid_factor_gco2_per_kwh=950"})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set: %v", err)
	}
	l1, err := acct.LoadLayer1("testctx")
	if err != nil {
		t.Fatal(err)
	}
	if l1.Emissions[acct.KeyPue] != "1.27" {
		t.Errorf("pue = %q, want 1.27", l1.Emissions[acct.KeyPue])
	}
	if l1.Emissions[acct.KeyGridFactor] != "950" {
		t.Errorf("grid_factor = %q, want 950", l1.Emissions[acct.KeyGridFactor])
	}

	cmd2 := newEmissionsCmd()
	cmd2.SetArgs([]string{"unset", "pue"})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetOut(&bytes.Buffer{})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("unset: %v", err)
	}
	l2, _ := acct.LoadLayer1("testctx")
	if _, has := l2.Emissions[acct.KeyPue]; has {
		t.Errorf("pue still present after unset")
	}
}

func TestEmissionsSetRejectsInvalidValues(t *testing.T) {
	withConfigDir(t)

	cases := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"set", "pue=0.5"}, "1.0"},
		{[]string{"set", "pue=4.0"}, "3.0"},
		{[]string{"set", "grid_factor_gco2_per_kwh=9999"}, "2000"},
		{[]string{"set", "cpu_w=-1"}, "must be ≥ 0"},
		{[]string{"set", "bogus=1"}, "unknown emissions key"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			cmd := newEmissionsCmd()
			cmd.SetArgs(c.args)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), c.wantErr)
			}
		})
	}
}
