package utils

import "testing"

func TestWithDefaultNomadHTTPPort(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"http://100.101.139.83", "http://100.101.139.83:4646"},
		{"http://100.101.139.83/", "http://100.101.139.83:4646"},
		{"http://100.101.139.83:4646", "http://100.101.139.83:4646"},
		{"http://100.101.139.83:9464", "http://100.101.139.83:9464"},
		{"https://nomad.example.com", "https://nomad.example.com"},
		{"https://nomad.example.com:4646", "https://nomad.example.com:4646"},
	}
	for _, tc := range tests {
		got := WithDefaultNomadHTTPPort(tc.in)
		if got != tc.want {
			t.Errorf("WithDefaultNomadHTTPPort(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeNomadAPIAddr(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"http://100.101.139.83", "http://100.101.139.83"},
		{"http://100.101.139.83/", "http://100.101.139.83"},
		{"http://100.101.139.83:4646", "http://100.101.139.83:4646"},
		{"http://100.101.139.83/v1", "http://100.101.139.83"},
		{"http://100.101.139.83:4646/v1", "http://100.101.139.83:4646"},
		{"https://nomad.example.com", "https://nomad.example.com"},
		{"https://nomad.example.com:4646", "https://nomad.example.com:4646"},
		{"http://10.0.2.40:4646", "http://10.0.2.40:4646"},
	}
	for _, tc := range tests {
		got := NormalizeNomadAPIAddr(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeNomadAPIAddr(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewNomadClient_NormalizesBareHTTPHost(t *testing.T) {
	c := NewNomadClient("http://100.101.139.83", "", "")
	if got := c.Addr(); got != "http://100.101.139.83:4646" {
		t.Fatalf("Addr: got %q want http://100.101.139.83:4646", got)
	}
}
