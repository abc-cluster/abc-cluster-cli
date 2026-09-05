package data

import (
	"strings"
	"testing"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// s5cmd refuses `rm s3://bucket/prefix/` outright ("forgot wildcard character?"),
// so --recursive has to produce the glob itself. Before this, --recursive was
// declared and registered but never read in RunE — passing it changed nothing and
// the user got s5cmd's wildcard message for a flag that claimed to do the job.

func TestExpandRecursiveTargets_PrefixGainsGlobUnderRecursive(t *testing.T) {
	got, err := expandRecursiveTargets([]string{"s3://su-abc-dev/check/"}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "s3://su-abc-dev/check/*"; got[0] != want {
		t.Errorf("got %q, want %q", got[0], want)
	}
}

func TestExpandRecursiveTargets_AmbiguousPathIsNotWidened(t *testing.T) {
	// "s3://bucket/check" with no trailing slash is genuinely ambiguous: it may
	// name an object OR a prefix, and nothing local can tell which. For a
	// destructive verb the ambiguity must resolve toward NOT widening — expanding
	// it to "check/*" would turn a one-object delete into a subtree delete on a
	// guess. The caller says which they meant with a trailing slash.
	got, err := expandRecursiveTargets([]string{"s3://su-abc-dev/check"}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0] != "s3://su-abc-dev/check" {
		t.Errorf("ambiguous path was widened to %q; expected it left alone", got[0])
	}
}

func TestExpandRecursiveTargets_BarePrefixWithoutRecursiveIsRefused(t *testing.T) {
	// Refused HERE, naming the flag — rather than passed down for s5cmd to
	// reject with wildcard trivia the caller cannot act on.
	_, err := expandRecursiveTargets([]string{"s3://su-abc-dev/check/"}, false)
	if err == nil {
		t.Fatal("expected an error for a bare prefix without --recursive")
	}
	if !strings.Contains(err.Error(), "--recursive") {
		t.Errorf("error should name --recursive, got: %v", err)
	}
}

func TestExpandRecursiveTargets_SingleObjectUntouched(t *testing.T) {
	// An object target must never gain a glob, with or without --recursive.
	for _, recursive := range []bool{false, true} {
		got, err := expandRecursiveTargets([]string{"s3://su-abc-dev/reads.bam"}, recursive)
		if err != nil {
			t.Fatalf("recursive=%v: unexpected error: %v", recursive, err)
		}
		if got[0] != "s3://su-abc-dev/reads.bam" {
			t.Errorf("recursive=%v: object target was rewritten to %q", recursive, got[0])
		}
	}
}

func TestExpandRecursiveTargets_ExplicitGlobPassesThrough(t *testing.T) {
	// The caller wrote the glob deliberately; do not double it.
	got, err := expandRecursiveTargets([]string{"s3://su-abc-dev/check/*"}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0] != "s3://su-abc-dev/check/*" {
		t.Errorf("explicit glob was rewritten to %q", got[0])
	}
}

func TestExpandRecursiveTargets_GlobCannotReachASibling(t *testing.T) {
	// The prefix bug this CLI shipped elsewhere was an over-broad delete, so be
	// explicit: expanding "check" must not produce something matching "check-out".
	got, err := expandRecursiveTargets([]string{"s3://su-abc-dev/check/"}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	glob := got[0]
	if strings.HasPrefix("s3://su-abc-dev/check-out/params.json", strings.TrimSuffix(glob, "*")) {
		t.Errorf("glob %q would also match the sibling prefix check-out/", glob)
	}
}

func TestIsPrefixTarget(t *testing.T) {
	cases := []struct {
		uri  string
		want bool
	}{
		{"s3://bucket/path/", true},    // trailing slash — a prefix
		{"s3://bucket", true},          // bare bucket — a prefix
		{"s3://bucket/obj.bam", false}, // an object
		{"s3://bucket/path/*", false},  // already globbed — caller's intent
		{"s3://bucket/p?th", false},    // any glob char
	}
	for _, c := range cases {
		if got := isPrefixTarget(c.uri); got != c.want {
			t.Errorf("isPrefixTarget(%q) = %v, want %v", c.uri, got, c.want)
		}
	}
}

// s5cmd's --dry-run is a GLOBAL flag. Placed after the subcommand it is rejected
// outright ("Incorrect Usage: flag provided but not defined: -dry-run") and s5cmd
// dumps its usage — so the preview on `abc data remove`, `purge` and `push` never
// worked, on verbs where previewing is the whole safety mechanism. These pin the
// position rather than the mere presence of the flag.

func TestS5cmdArgs_DryRunPrecedesTheSubcommand(t *testing.T) {
	args := s5cmdArgs(abccfg.Context{}, "rm", []string{"s3://b/p/*"}, "--dry-run")

	subIdx, flagIdx := -1, -1
	for i, a := range args {
		switch a {
		case "rm":
			subIdx = i
		case "--dry-run":
			flagIdx = i
		}
	}
	if flagIdx == -1 || subIdx == -1 {
		t.Fatalf("expected both --dry-run and rm in %q", args)
	}
	if flagIdx > subIdx {
		t.Errorf("--dry-run must precede the subcommand; got %q", args)
	}
}

func TestS5cmdArgs_DryRunPrecedesCpSubcommand(t *testing.T) {
	args := s5cmdArgs(abccfg.Context{}, "cp", []string{"/tmp/a", "s3://b/k"}, "--dry-run")
	for i, a := range args {
		if a == "cp" {
			for _, later := range args[i+1:] {
				if later == "--dry-run" {
					t.Fatalf("--dry-run appears after cp; got %q", args)
				}
			}
		}
	}
}
