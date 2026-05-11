package utils

import (
	"bytes"
	"strings"
	"testing"
)

func TestUpsertEnvHonouringSelector_LocalShellWins(t *testing.T) {
	ResetShadowingWarningsForTest()
	base := []string{"PATH=/bin", "MINIO_ROOT_USER=alice"}
	stderr := &bytes.Buffer{}
	got := UpsertEnvHonouringSelector(base,
		map[string]string{"MINIO_ROOT_USER": "bob"},
		"local", stderr)

	if v := GetenvFromEnviron(got, "MINIO_ROOT_USER"); v != "alice" {
		t.Errorf("--config local: shell should win; got MINIO_ROOT_USER=%q want alice", v)
	}
	if stderr.Len() != 0 {
		t.Errorf("--config local: no warning expected; got %q", stderr.String())
	}
}

func TestUpsertEnvHonouringSelector_NomadForcesOverwriteWithWarning(t *testing.T) {
	ResetShadowingWarningsForTest()
	base := []string{"PATH=/bin", "MINIO_ROOT_USER=alice"}
	stderr := &bytes.Buffer{}
	got := UpsertEnvHonouringSelector(base,
		map[string]string{"MINIO_ROOT_USER": "bob"},
		"nomad", stderr)

	if v := GetenvFromEnviron(got, "MINIO_ROOT_USER"); v != "bob" {
		t.Errorf("--config nomad: resolved value should win; got MINIO_ROOT_USER=%q want bob", v)
	}
	if !strings.Contains(stderr.String(), "MINIO_ROOT_USER is set in shell but ignored") {
		t.Errorf("expected shadowing warning, got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--config nomad") {
		t.Errorf("warning should cite selector; got: %q", stderr.String())
	}
}

func TestUpsertEnvHonouringSelector_VaultSimilarToNomad(t *testing.T) {
	ResetShadowingWarningsForTest()
	base := []string{"VAULT_TOKEN=shell-tok"}
	stderr := &bytes.Buffer{}
	got := UpsertEnvHonouringSelector(base,
		map[string]string{"VAULT_TOKEN": "vault-resolved-tok"},
		"vault", stderr)

	if v := GetenvFromEnviron(got, "VAULT_TOKEN"); v != "vault-resolved-tok" {
		t.Errorf("--config vault: resolved value should win; got VAULT_TOKEN=%q", v)
	}
	if !strings.Contains(stderr.String(), "--config vault") {
		t.Errorf("warning should cite vault selector; got: %q", stderr.String())
	}
}

func TestUpsertEnvHonouringSelector_AgreeingValuesProduceNoWarning(t *testing.T) {
	ResetShadowingWarningsForTest()
	// Shell and config agree → no warning even with --config nomad.
	base := []string{"MINIO_ROOT_USER=alice"}
	stderr := &bytes.Buffer{}
	UpsertEnvHonouringSelector(base,
		map[string]string{"MINIO_ROOT_USER": "alice"},
		"nomad", stderr)
	if stderr.Len() != 0 {
		t.Errorf("agreeing values should not warn; got %q", stderr.String())
	}
}

func TestUpsertEnvHonouringSelector_WarnOncePerKey(t *testing.T) {
	ResetShadowingWarningsForTest()
	base := []string{"MINIO_ROOT_USER=alice"}
	stderr := &bytes.Buffer{}
	UpsertEnvHonouringSelector(base,
		map[string]string{"MINIO_ROOT_USER": "bob"}, "nomad", stderr)
	UpsertEnvHonouringSelector(base,
		map[string]string{"MINIO_ROOT_USER": "bob"}, "nomad", stderr)
	UpsertEnvHonouringSelector(base,
		map[string]string{"MINIO_ROOT_USER": "bob"}, "nomad", stderr)
	count := strings.Count(stderr.String(), "MINIO_ROOT_USER is set in shell but ignored")
	if count != 1 {
		t.Errorf("expected 1 warning across 3 calls, got %d: %q", count, stderr.String())
	}
}

func TestUpsertEnvHonouringSelector_UnsetShellIsSilent(t *testing.T) {
	ResetShadowingWarningsForTest()
	// Shell has no value → resolved value wins with no warning (nothing
	// to shadow).
	base := []string{"PATH=/bin"}
	stderr := &bytes.Buffer{}
	got := UpsertEnvHonouringSelector(base,
		map[string]string{"MINIO_ROOT_USER": "bob"}, "nomad", stderr)
	if v := GetenvFromEnviron(got, "MINIO_ROOT_USER"); v != "bob" {
		t.Errorf("unset shell: resolved should win; got %q", v)
	}
	if stderr.Len() != 0 {
		t.Errorf("unset shell: no warning expected; got %q", stderr.String())
	}
}
