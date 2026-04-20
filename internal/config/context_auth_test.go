package config

import "testing"

func TestAuthWhoamiSetGetUnset(t *testing.T) {
	t.Parallel()
	c := &Config{
		Version:       CurrentVersion,
		ActiveContext: "lab",
		Contexts: map[string]Context{
			"lab": {Endpoint: "https://example.invalid"},
		},
	}
	if err := c.Set("contexts.lab.auth.whoami", "token-user"); err != nil {
		t.Fatal(err)
	}
	v, ok := c.Get("contexts.lab.auth.whoami")
	if !ok || v != "token-user" {
		t.Fatalf("get: ok=%v v=%q", ok, v)
	}
	if err := c.Unset("contexts.lab.auth.whoami"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("contexts.lab.auth.whoami"); ok {
		t.Fatal("expected whoami unset")
	}
}
