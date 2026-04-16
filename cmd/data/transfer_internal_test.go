package data

import (
	"strings"
	"testing"
)

func TestBuildRcloneNomadScript(t *testing.T) {
	s, err := buildRcloneNomadScript("[m]\ntype=s3\n", "m:src/a", "m:dst/b", "copy", 3, true, "--checksum")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "rclone copy") {
		t.Fatalf("expected rclone copy, got:\n%s", s)
	}
	if !strings.Contains(s, "--dry-run") {
		t.Fatal("expected dry-run")
	}
	if !strings.Contains(s, "--checksum") {
		t.Fatal("expected tool-args")
	}
	if !strings.Contains(s, "[m]") {
		t.Fatal("expected ini in heredoc")
	}
}

func TestBuildRcloneNomadScript_EmptyConfig(t *testing.T) {
	_, err := buildRcloneNomadScript("", "a", "b", "move", 1, false, "")
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}
