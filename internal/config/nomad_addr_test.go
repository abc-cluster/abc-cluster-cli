package config

import (
	"testing"
)

func TestCanonicalNomadAPIAddrForYAML(t *testing.T) {
	t.Parallel()
	if got := CanonicalNomadAPIAddrForYAML("http://10.0.0.1"); got != "http://10.0.0.1:4646" {
		t.Fatalf("got %q", got)
	}
	if got := CanonicalNomadAPIAddrForYAML("http://10.0.0.1:4646/v1"); got != "http://10.0.0.1:4646" {
		t.Fatalf("got %q", got)
	}
	if got := CanonicalNomadAPIAddrForYAML("https://nomad.example.com"); got != "https://nomad.example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateNomadAddrForContext(t *testing.T) {
	if err := ValidateNomadAddrForContext(""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNomadAddrForContext("http://10.0.0.1:4646"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNomadAddrForContext("http://10.0.0.1:9464"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNomadAddrForContext("https://nomad.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNomadAddrForContext("https://nomad.example.com:8443"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNomadAddrForContext("http://10.0.0.1"); err == nil {
		t.Fatal("expected error for http without port")
	}
	if err := ValidateNomadAddrForContext("not-a-url"); err == nil {
		t.Fatal("expected error for invalid URL")
	}
}
