package utils

import "testing"

func TestWhoamiSlug(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// Single-word names: first 5 chars.
		{"abhi", "abhi"},
		{"alice", "alice"},
		{"management", "manag"},
		{"researcher", "resea"},

		// Two-segment names: 3 chars each, joined, truncated to 5.
		{"group-admin", "groad"},
		{"group-leader", "grole"},   // distinct from group-admin
		{"cluster-admin", "cluad"},
		{"cluster-leader", "clule"}, // distinct from cluster-admin
		{"abhi-admin", "abhad"},

		// Colon-separated: use rightmost segment only.
		{"abc:default:cluster-admin", "cluad"},
		{"abc:su-mbhg-hostgen:researcher", "resea"},
		{"abc:su-mbhg-hostgen:group-admin", "groad"},
		{"abc:su-mbhg-hostgen:group-leader", "grole"},

		// Three-segment: ceil(5/3)=2 chars each → 6 chars → truncate to 5.
		{"group-super-admin", "grsua"},

		// Short / edge cases.
		{"a", "a"},
		{"12345678", "12345"},
		{"", ""},
		{":::", ""},
		{"abc::", ""},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := WhoamiSlug(tc.input)
			if got != tc.want {
				t.Errorf("WhoamiSlug(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
