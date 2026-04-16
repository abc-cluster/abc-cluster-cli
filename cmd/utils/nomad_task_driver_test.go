package utils

import "testing"

func TestNormalizeNomadTaskDriver(t *testing.T) {
	if got := NormalizeNomadTaskDriver("containerd"); got != "containerd-driver" {
		t.Fatalf("containerd: got %q want containerd-driver", got)
	}
	if got := NormalizeNomadTaskDriver("CONTAINERD"); got != "containerd-driver" {
		t.Fatalf("CONTAINERD: got %q want containerd-driver", got)
	}
	if got := NormalizeNomadTaskDriver("containerd-driver"); got != "containerd-driver" {
		t.Fatalf("containerd-driver: got %q want unchanged", got)
	}
	if got := NormalizeNomadTaskDriver("docker"); got != "docker" {
		t.Fatalf("docker: got %q want docker", got)
	}
	if got := NormalizeNomadTaskDriver("  exec  "); got != "exec" {
		t.Fatalf("exec trim: got %q want exec", got)
	}
	if got := NormalizeNomadTaskDriver("RAW_EXEC"); got != "raw_exec" {
		t.Fatalf("raw_exec: got %q want raw_exec", got)
	}
}
