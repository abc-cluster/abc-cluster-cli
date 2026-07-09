package job

import "testing"

func TestParseVolumeFlag(t *testing.T) {
	cases := []struct {
		in       string
		vol, dst string
		ro       bool
		wantErr  bool
	}{
		{"abc-tools", "abc-tools", "/mnt/abc-tools", false, false},
		{"abc-tools:/opt/abc-tools", "abc-tools", "/opt/abc-tools", false, false},
		{"abc-tools:/opt/abc-tools:ro", "abc-tools", "/opt/abc-tools", true, false},
		{"data:ro", "data", "/mnt/data", true, false},
		{"data:rw", "data", "/mnt/data", false, false},
		{":/x", "", "", false, true},           // empty name
		{"data:relative", "", "", false, true}, // non-absolute dest
	}
	for _, c := range cases {
		m, err := parseVolumeFlag(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error, got %+v", c.in, m)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if m.Volume != c.vol || m.Dest != c.dst || m.ReadOnly != c.ro {
			t.Errorf("%q: got %+v, want vol=%s dst=%s ro=%v", c.in, m, c.vol, c.dst, c.ro)
		}
	}
}
