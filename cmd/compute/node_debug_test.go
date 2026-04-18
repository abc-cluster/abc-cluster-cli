package compute

import (
	"strings"
	"testing"
)

func TestLinuxNodeDebugScriptBody(t *testing.T) {
	t.Parallel()
	s := linuxNodeDebugScriptBody()
	if !strings.Contains(s, "/sys/module/bridge") {
		t.Fatal("expected bridge sysfs check")
	}
	if !strings.Contains(s, cniPluginsInstallDir) {
		t.Fatal("expected cni path in script")
	}
	if !strings.Contains(s, "journalctl -u nomad") {
		t.Fatal("expected nomad journal grep")
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	in := "a'b\nc"
	q := shellQuote(in)
	if !strings.HasPrefix(q, "'") || !strings.HasSuffix(q, "'") {
		t.Fatalf("expected single-quoted: %q", q)
	}
	if !strings.Contains(q, "'\"'\"'") {
		t.Fatalf("expected escaped single quote: %q", q)
	}
}
