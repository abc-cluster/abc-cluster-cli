package config

import "testing"

func TestDeriveUploadEndpointFromAPI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"https://api.example.com", "https://api.example.com/files/"},
		{"https://api.example.com/", "https://api.example.com/files/"},
		{"https://api.example.com/v1", "https://api.example.com/v1/files/"},
		{"https://api.example.com/v1/", "https://api.example.com/v1/files/"},
		{"https://api.example.com/v1//", "https://api.example.com/v1/files/"},
		{"https://api.example.com/files", "https://api.example.com/files/"},
		{"https://api.example.com/files/", "https://api.example.com/files/"},
	}
	for _, tc := range cases {
		got, err := DeriveUploadEndpointFromAPI(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("DeriveUploadEndpointFromAPI(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestDeriveUploadEndpointFromAPI_errors(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "not-a-url", "http://"} {
		if _, err := DeriveUploadEndpointFromAPI(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}
