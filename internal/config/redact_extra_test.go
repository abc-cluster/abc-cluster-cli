package config

import "testing"

// B8: `abc config set` / `config list` must never echo credential material.
func TestRedactSensitiveFields_Sensitive(t *testing.T) {
	full := []struct{ key, val string }{
		{"contexts.x.admin.services.minio.secret_key", "ace63c7a-4f95-155"},
		{"contexts.x.admin.services.minio.access_key", "abhi"},
		{"admin.services.pg.password", "hunter2"},
		{"admin.services.nomad.token", "s3cr3t-token"},
		{"admin.services.foo.api_key", "k-123"},
	}
	for _, c := range full {
		got, red := RedactSensitiveFields(c.key, c.val)
		if !red || got != "<redacted>" {
			t.Errorf("%s: got (%q,%v), want (<redacted>,true)", c.key, got, red)
		}
	}
}

func TestRedactSensitiveFields_TokenAndPlain(t *testing.T) {
	// access_token keeps a recognizable prefix (not fully redacted, not raw).
	if got, red := RedactSensitiveFields("contexts.x.access_token", "abcdefgh12345678"); !red || got == "<redacted>" || got == "abcdefgh12345678" {
		t.Errorf("access_token: got (%q,%v), want partial-mask+redacted", got, red)
	}
	// Non-sensitive keys pass through untouched.
	for _, c := range []struct{ key, val string }{
		{"contexts.x.realm", "aither"},
		{"admin.services.minio.endpoint", "http://10.0.0.1:9000"},
	} {
		if got, red := RedactSensitiveFields(c.key, c.val); red || got != c.val {
			t.Errorf("%s: got (%q,%v), want (%q,false)", c.key, got, red, c.val)
		}
	}
}
