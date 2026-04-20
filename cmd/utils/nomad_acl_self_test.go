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
