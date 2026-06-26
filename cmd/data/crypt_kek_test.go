package data

import (
	"testing"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func TestResolveManagedContext(t *testing.T) {
	cfg := &abccfg.Config{
		ActiveContext:  "alpha",
		ContextAliases: map[string]string{},
		Contexts: map[string]abccfg.Context{
			"alpha": {Namespace: "su-grpA", AccessToken: "ta"},
			"grpB":  {Namespace: "su-grpB", AccessToken: "tb"},
		},
	}

	// no --group → active context, no cross-check kek_id (broker derives).
	n, _, kek, err := resolveManagedContext(cfg, "")
	if err != nil || n != "alpha" || kek != "" {
		t.Fatalf("active: name=%q kek=%q err=%v", n, kek, err)
	}

	// --group grpA → matched by namespace su-grpA; cross-check kek_id sent.
	n, _, kek, err = resolveManagedContext(cfg, "grpA")
	if err != nil || n != "alpha" || kek != "group:grpA" {
		t.Fatalf("by namespace: name=%q kek=%q err=%v", n, kek, err)
	}

	// --group grpB → matched by context name (also its namespace); one match.
	n, _, kek, err = resolveManagedContext(cfg, "grpB")
	if err != nil || n != "grpB" || kek != "group:grpB" {
		t.Fatalf("by name: name=%q kek=%q err=%v", n, kek, err)
	}

	// unknown group → a clear not-found error listing the available contexts.
	if _, _, _, err := resolveManagedContext(cfg, "nope"); err == nil {
		t.Fatal("expected a not-found error for an unknown group")
	}
}
