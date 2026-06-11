package appgen

import (
	"strings"
	"testing"
)

func TestMarshalCanonical_VersionFirstAndOrdered(t *testing.T) {
	s := validSucuriSpec()
	s.Exposure = "internal"
	s.Env = map[string]string{"ZED": "1", "ALPHA": "2", "MID": "3"}
	if err := s.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	s.ApplyDefaults()

	out, err := s.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	y := string(out)

	// version must be the first key.
	if first := strings.SplitN(strings.TrimSpace(y), "\n", 2)[0]; !strings.HasPrefix(first, "version:") {
		t.Errorf("first line is %q, want version: ...\n%s", first, y)
	}
	if !strings.Contains(y, "version: \"1.0\"") {
		t.Errorf("version not stamped:\n%s", y)
	}
	// env keys must be sorted (ALPHA < MID < ZED).
	ia, im, iz := strings.Index(y, "ALPHA"), strings.Index(y, "MID"), strings.Index(y, "ZED")
	if !(ia < im && im < iz) {
		t.Errorf("env keys not sorted (ALPHA<MID<ZED): %d %d %d\n%s", ia, im, iz, y)
	}
	// top-level field order: version < name < project < framework < image.
	order := []string{"version:", "name:", "project:", "framework:", "image:", "expose:"}
	prev := -1
	for _, k := range order {
		i := strings.Index(y, "\n"+k)
		if i < 0 {
			i = 0
			if strings.HasPrefix(y, k) {
				i = 0
			} else {
				t.Fatalf("missing key %q:\n%s", k, y)
			}
		}
		if i < prev {
			t.Errorf("key %q out of canonical order:\n%s", k, y)
		}
		prev = i
	}
	// must round-trip back through the strict parser.
	if _, err := Parse(out); err != nil {
		t.Errorf("canonical YAML does not re-parse: %v\n%s", err, y)
	}
}
