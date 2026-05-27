package utils

import "testing"

func TestNomadWhoamiLabelFromACLToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tok  *NomadACLToken
		want string
	}{
		{"nil", nil, ""},
		{"named", &NomadACLToken{Name: "  lab-op  "}, "lab-op"},
		// pool- prefix stripped — the bare slot is the identity used in
		// path segments (workdir, results, upload destination, job IDs).
		// The pool- prefix is operator metadata for Nomad UI distinction
		// only. Mover python applies the same stripping for upload paths.
		{"pool token", &NomadACLToken{Name: "pool-lunar_hornbill"}, "lunar_hornbill"},
		{"pool token with spaces", &NomadACLToken{Name: "  pool-slate_sunbird  "}, "slate_sunbird"},
		{"non-pool 'pool-like' name preserved through trim", &NomadACLToken{Name: "pool"}, "pool"},
		{"management", &NomadACLToken{Type: "management"}, "management"},
		{"policies", &NomadACLToken{Type: "client", Policies: []string{"a", "b"}}, "a,b"},
		{"accessor", &NomadACLToken{AccessorID: "abcdef12-0000-0000-0000-000000000000"}, "token:abcdef12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NomadWhoamiLabelFromACLToken(tc.tok); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
