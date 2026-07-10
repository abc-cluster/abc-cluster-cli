package data

import "testing"

// B15: pushing a local directory normalizes to "contents into prefix" (both
// sides end with "/") so recursion is predictable and never errors or doubles.
func TestNormalizePushDir(t *testing.T) {
	cases := []struct{ inSrc, inDst, wSrc, wDst string }{
		{"/tmp/top", "s3://b/results", "/tmp/top/", "s3://b/results/"},
		{"/tmp/top/", "s3://b/results/", "/tmp/top/", "s3://b/results/"},
		{"/tmp/top", "s3://b/results/", "/tmp/top/", "s3://b/results/"},
	}
	for _, c := range cases {
		gs, gd := normalizePushDir(c.inSrc, c.inDst)
		if gs != c.wSrc || gd != c.wDst {
			t.Errorf("normalizePushDir(%q,%q) = (%q,%q), want (%q,%q)", c.inSrc, c.inDst, gs, gd, c.wSrc, c.wDst)
		}
	}
}

// B15: `pull` accepts the destination positionally (symmetric with push) and via
// --destination; supplying both conflictingly is an error.
func TestResolvePullDest(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		flagDest string
		want     string
		wantErr  bool
	}{
		{"no-dest", []string{"s3://b/x"}, "", "", false},
		{"flag-only", []string{"s3://b/x"}, "/tmp/out", "/tmp/out", false},
		{"positional-only", []string{"s3://b/x", "/tmp/out"}, "", "/tmp/out", false},
		{"both-agree", []string{"s3://b/x", "/tmp/out"}, "/tmp/out", "/tmp/out", false},
		{"both-conflict", []string{"s3://b/x", "/tmp/a"}, "/tmp/b", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolvePullDest(c.args, c.flagDest)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got dest %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
