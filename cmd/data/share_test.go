package data

import "testing"

func TestIsValidShareName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// happy paths
		{"results.vcf", true},
		{"refs/grch38.fa", true},
		{"a/b/c/file.txt", true},
		{"file-with-dashes_and_underscores.tsv", true},
		// rejects
		{"", false},
		{"/etc/passwd", false}, // absolute
		{"/leading", false},    // leading slash
		{"../escape", false},   // parent traversal
		{"a/../b", false},      // embedded ..
		{"a/./b", false},       // embedded .
		{"a//b", false},        // empty segment
		{"..", false},          // bare ..
		{".", false},           // bare .
		{"a/", false},          // trailing-slash empty segment
	}
	for _, c := range cases {
		got := isValidShareName(c.in)
		if got != c.want {
			t.Errorf("isValidShareName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
