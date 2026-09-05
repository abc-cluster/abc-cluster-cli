package data

import (
	"strings"
	"testing"
)

// TestBuildPushCpArgs guards bugs G-D / G-E: s5cmd `cp` flags must precede the
// source/destination positionals, and the skip-if-unchanged flag must be the
// real s5cmd flag `--if-size-differ` (NOT the non-existent `--if-checksum-differ`).
//
// --dry-run is deliberately absent here: it is a GLOBAL s5cmd flag and is added
// before the subcommand by the caller. Emitting it among the `cp` flags made
// s5cmd reject it as undefined ("flag provided but not defined: -dry-run").
func TestBuildPushCpArgs(t *testing.T) {
	const src, dst = "/tmp/a.bin", "s3://bucket/key"

	cases := []struct {
		name     string
		checksum bool
		want     []string
	}{
		{"plain", false, []string{src, dst}},
		{"checksum", true, []string{"--if-size-differ", src, dst}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildPushCpArgs(c.checksum, src, dst)
			if strings.Join(got, " ") != strings.Join(c.want, " ") {
				t.Fatalf("got %q want %q", got, c.want)
			}
			// Flags must come before positionals: once we see a non-flag arg,
			// no flag may follow.
			seenPositional := false
			for _, a := range got {
				if strings.HasPrefix(a, "-") {
					if seenPositional {
						t.Fatalf("flag %q appears after a positional in %q", a, got)
					}
				} else {
					seenPositional = true
				}
			}
			// The bogus flag must never be emitted.
			for _, a := range got {
				if a == "--if-checksum-differ" {
					t.Fatalf("emitted non-existent s5cmd flag --if-checksum-differ: %q", got)
				}
			}
		})
	}
}
