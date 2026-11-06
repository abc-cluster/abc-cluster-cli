package secretref

import "testing"

// TemplateRef must keep emitting exactly what `abc secrets ref` and
// `pipeline run` secret:// translation produced before this package existed.
func TestTemplateRef_MatchesLegacyFormat(t *testing.T) {
	if got, want := TemplateRef(BackendNomad, "lab", "k"),
		`{{ with nomadVar "abc/secrets/lab/k" }}{{ index . "value" }}{{ end }}`; got != want {
		t.Errorf("nomad: got %q want %q", got, want)
	}
	if got, want := TemplateRef(BackendVault, "lab", "k"),
		`{{ with secret "secret/data/abc/lab/k" }}{{ .Data.data.value }}{{ end }}`; got != want {
		t.Errorf("vault: got %q want %q", got, want)
	}
	if got := TemplateRef(BackendNomad, "", "k"); got != `{{ with nomadVar "abc/secrets/default/k" }}{{ index . "value" }}{{ end }}` {
		t.Errorf("empty namespace should default: %q", got)
	}
}

func TestTemplateRefJSON(t *testing.T) {
	if got, want := TemplateRefJSON(BackendNomad, "lab", "k"),
		`{{ with nomadVar "abc/secrets/lab/k" }}{{ index . "value" | printf "%s" | toJSON }}{{ end }}`; got != want {
		t.Errorf("nomad: got %q want %q", got, want)
	}
	if got, want := TemplateRefJSON(BackendVault, "lab", "k"),
		`{{ with secret "secret/data/abc/lab/k" }}{{ .Data.data.value | printf "%s" | toJSON }}{{ end }}`; got != want {
		t.Errorf("vault: got %q want %q", got, want)
	}
}

func TestParseURI(t *testing.T) {
	for in, want := range map[string]string{
		"secret://k":      "k",
		"  secret:// k  ": "k",
		"secret://":       "",
		"k":               "",
		"s3://bucket":     "",
	} {
		got, ok := ParseURI(in)
		if got != want || ok != (want != "") {
			t.Errorf("ParseURI(%q) = %q,%v; want %q,%v", in, got, ok, want, want != "")
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"k", "acacia-sk", "a.b_c-1", "RCLONE"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-k", "a/b", "a b", "{{x}}", `a"b`, ".."} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) expected error", bad)
		}
	}
}

func TestBackendFor(t *testing.T) {
	for in, want := range map[string]string{"": BackendNomad, "nomad": BackendNomad, "vault": BackendVault, "vault+sealed": BackendVault, "none": BackendNomad} {
		if got := BackendFor(in); got != want {
			t.Errorf("BackendFor(%q) = %q want %q", in, got, want)
		}
	}
}
