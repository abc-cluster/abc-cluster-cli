package selfupdate

import "testing"

func TestBaseSemverTag(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"clean release", "v0.1.33", "v0.1.33"},
		{"no v prefix", "0.1.33", "v0.1.33"},
		{"git-describe suffix", "v0.1.33-4-g572e7b7", "v0.1.33"},
		{"git-describe + commit paren", "v0.1.33-4-g572e7b7 (572e7b7)", "v0.1.33"},
		{"clean release + commit paren", "v0.1.33 (572e7b7)", "v0.1.33"},
		{"dev build", "dev", ""},
		{"dev build with commit", "dev (abcdef0)", ""},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"garbage", "not-a-version", ""},
		{"canonicalises", "v1.2", "v1.2.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := baseSemverTag(tc.raw); got != tc.want {
				t.Errorf("baseSemverTag(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestAlreadyCurrent(t *testing.T) {
	cases := []struct {
		name    string
		current string
		target  string
		pinned  bool
		want    bool
	}{
		{"equal, not pinned", "v0.1.33", "v0.1.33", false, true},
		{"current ahead, not pinned", "v0.2.0", "v0.1.33", false, true},
		{"current behind, not pinned", "v0.1.32", "v0.1.33", false, false},
		{"equal but pinned (re-pin allowed)", "v0.1.33", "v0.1.33", true, false},
		{"ahead but pinned (downgrade allowed)", "v0.2.0", "v0.1.0", true, false},
		{"dev build current (no tag)", "", "v0.1.33", false, false},
		{"invalid current tag", "garbage", "v0.1.33", false, false},
		{"invalid target tag", "v0.1.33", "garbage", false, false},
		{"patch behind", "v0.1.33", "v0.1.34", false, false},
		{"minor behind", "v0.1.99", "v0.2.0", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alreadyCurrent(tc.current, tc.target, tc.pinned); got != tc.want {
				t.Errorf("alreadyCurrent(%q, %q, pinned=%v) = %v, want %v",
					tc.current, tc.target, tc.pinned, got, tc.want)
			}
		})
	}
}

func TestDisplayVersion(t *testing.T) {
	cases := []struct {
		raw  string
		tag  string
		want string
	}{
		{"v0.1.33-4-g572e7b7 (572e7b7)", "v0.1.33", "v0.1.33-4-g572e7b7 (572e7b7)"},
		{"", "v0.1.33", "v0.1.33"},
		{"  ", "v0.1.33", "v0.1.33"},
		{"v0.1.33", "v0.1.33", "v0.1.33"},
	}
	for _, tc := range cases {
		if got := displayVersion(tc.raw, tc.tag); got != tc.want {
			t.Errorf("displayVersion(%q, %q) = %q, want %q", tc.raw, tc.tag, got, tc.want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		bytes int
		want  string
	}{
		{0, "size unknown"},
		{-5, "size unknown"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{21389496, "20.4 MiB"}, // real abc-linux-amd64 size
		{1073741824, "1.0 GiB"},
	}
	for _, tc := range cases {
		if got := humanSize(tc.bytes); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
