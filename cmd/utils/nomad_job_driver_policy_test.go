package utils

import (
	"strings"
	"testing"
)

func TestPlatformDriverPolicy_IsPrivileged(t *testing.T) {
	p := DefaultPlatformDriverPolicy()
	cases := []struct {
		name string
		tok  *NomadACLToken
		want bool
	}{
		{"nil token", nil, false},
		{"management type", &NomadACLToken{Type: "management"}, true},
		{"management mixed case", &NomadACLToken{Type: "Management"}, true},
		{"named privileged user gvds", &NomadACLToken{Type: "client", Name: "gvds"}, true},
		{"named privileged user abhi", &NomadACLToken{Type: "client", Name: "abhi"}, true},
		{"named privileged user jorge", &NomadACLToken{Type: "client", Name: "jorge"}, true},
		{"named privileged user uppercase", &NomadACLToken{Type: "client", Name: "JORGE"}, true},
		{"r-multi-group-admin role", &NomadACLToken{Type: "client", Name: "future-admin",
			Roles: []NomadACLRole{{Name: "r-multi-group-admin"}}}, true},
		{"per-group admin only", &NomadACLToken{Type: "client", Name: "anel",
			Roles: []NomadACLRole{{Name: "r-su-mbhg-hostgen-admin"}}}, false},
		{"pool slot", &NomadACLToken{Type: "client", Name: "pool-brave_pangolin",
			Roles: []NomadACLRole{{Name: "r-su-mbhg-bioinformatics-pool"}}}, false},
		{"unknown user", &NomadACLToken{Type: "client", Name: "stranger"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.IsPrivileged(c.tok); got != c.want {
				t.Fatalf("IsPrivileged(%+v)=%v, want %v", c.tok, got, c.want)
			}
		})
	}
}

func TestPlatformDriverPolicy_CheckDrivers(t *testing.T) {
	p := DefaultPlatformDriverPolicy()
	anel := &NomadACLToken{Type: "client", Name: "anel",
		Roles: []NomadACLRole{{Name: "r-su-mbhg-hostgen-admin"}}}
	gvds := &NomadACLToken{Type: "client", Name: "gvds",
		Roles: []NomadACLRole{{Name: "r-multi-group-admin"}}}
	mgmt := &NomadACLToken{Type: "management", Name: "abhi"}

	cases := []struct {
		name    string
		tok     *NomadACLToken
		drivers []string
		want    []string
	}{
		{"per-group admin allowed docker+exec",
			anel, []string{"docker", "exec"}, nil},
		{"per-group admin rejected raw_exec",
			anel, []string{"raw_exec"}, []string{"raw_exec"}},
		{"per-group admin rejected mix",
			anel, []string{"docker", "raw_exec", "java"}, []string{"java", "raw_exec"}},
		{"per-group admin rejected dedup",
			anel, []string{"raw_exec", "raw_exec"}, []string{"raw_exec"}},
		{"per-group admin rejected hpc-bridge",
			anel, []string{"hpc-bridge"}, []string{"hpc-bridge"}},
		{"multi-group admin bypass (gvds)",
			gvds, []string{"raw_exec", "containerd-driver"}, nil},
		{"management bypass",
			mgmt, []string{"raw_exec", "java", "qemu"}, nil},
		{"empty list",
			anel, nil, nil},
		{"unknown driver also rejected for non-priv",
			anel, []string{"some-future-driver"}, []string{"some-future-driver"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := p.CheckDrivers(c.tok, c.drivers)
			if !equalStrings(got, c.want) {
				t.Fatalf("CheckDrivers got=%v, want=%v", got, c.want)
			}
		})
	}
}

func TestPlatformDriverPolicy_AllowedList(t *testing.T) {
	p := DefaultPlatformDriverPolicy()
	got := strings.Join(p.AllowedList(), ",")
	if got != "docker,exec" {
		t.Fatalf("AllowedList=%q, want %q", got, "docker,exec")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
