package config

import "testing"

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
