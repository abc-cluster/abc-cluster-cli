package utils

import (
	"strings"
	"testing"
)

func TestGetenvFromEnviron(t *testing.T) {
	env := []string{"A=1", "B=x=y", "EMPTY="}
	if got := GetenvFromEnviron(env, "B"); got != "x=y" {
		t.Fatalf("B: %q", got)
	}
	if got := GetenvFromEnviron(env, "MISSING"); got != "" {
		t.Fatalf("missing: %q", got)
	}
}

func TestUpsertEnvOnlyMissing_SkipsWhenSet(t *testing.T) {
	base := []string{"AWS_ACCESS_KEY_ID=from-shell"}
	extras := map[string]string{
		"AWS_ACCESS_KEY_ID":     "from-config",
		"AWS_SECRET_ACCESS_KEY": "secret",
	}
	out := UpsertEnvOnlyMissing(base, extras)
	if !strings.Contains(strings.Join(out, "\n"), "AWS_ACCESS_KEY_ID=from-shell") {
		t.Fatalf("should keep existing access key: %v", out)
	}
	if !containsEnv(out, "AWS_SECRET_ACCESS_KEY=secret") {
		t.Fatalf("should add secret: %v", out)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func TestExtractBinaryLocationFlag_ExplicitDoubleDash(t *testing.T) {
	bin, child, err := ExtractBinaryLocationFlag([]string{
		"--binary-location", "/opt/nomad", "--", "job", "status", "-short",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/opt/nomad" {
		t.Fatalf("bin: %q", bin)
	}
	want := []string{"job", "status", "-short"}
	if len(child) != len(want) {
		t.Fatalf("child=%v", child)
	}
	for i := range want {
		if child[i] != want[i] {
			t.Fatalf("child[%d]=%q want %q", i, child[i], want[i])
		}
	}
}

func TestExtractBinaryLocationFlag_ExplicitDoubleDashPreservesChildDashDash(t *testing.T) {
	_, child, err := ExtractBinaryLocationFlag([]string{"--", "alloc", "logs", "--", "-tail"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alloc", "logs", "--", "-tail"}
	if len(child) != len(want) {
		t.Fatalf("child=%v want %v", child, want)
	}
	for i := range want {
		if child[i] != want[i] {
			t.Fatalf("child[%d]=%q want %q", i, child[i], want[i])
		}
	}
}

func TestExtractBinaryLocationFlag_NoLeadingBinaryPassesThroughIncludingMidDashDash(t *testing.T) {
	bin, child, err := ExtractBinaryLocationFlag([]string{"job", "logs", "--", "-tail"})
	if err != nil {
		t.Fatal(err)
	}
	if bin != "" {
		t.Fatalf("unexpected bin %q", bin)
	}
	want := []string{"job", "logs", "--", "-tail"}
	if len(child) != len(want) {
		t.Fatalf("child=%v want %v", child, want)
	}
	for i := range want {
		if child[i] != want[i] {
			t.Fatalf("child[%d]=%q want %q", i, child[i], want[i])
		}
	}
}
