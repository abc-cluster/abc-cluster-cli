package appgen

import (
	"strings"
	"testing"
)

func validSucuriSpec() *Spec {
	return &Spec{
		Name:      "sucuri",
		Image:     "ghcr.io/biosharp-dotnet/sucuri-api:latest",
		Project:   "abc-platform",
		Framework: "pode",
		Port:      8085,
		Health:    "/health/ready",
		Access:    "team",
		Env: map[string]string{
			"SUCURI_ENVIRONMENT": "production",
			"SUCURI_LOG_LEVEL":   "Warning",
		},
		Resources: Resources{CPU: 500, Memory: 256},
	}
}

func TestValidate_Valid(t *testing.T) {
	s := validSucuriSpec()
	if err := s.Validate(); err != nil {
		t.Fatalf("expected valid spec, got: %v", err)
	}
}

func TestValidate_NameRules(t *testing.T) {
	cases := map[string]string{
		"missing name": "",
		"uppercase":    "Sucuri",
		"underscore":   "su_curi",
		"space":        "su curi",
		"too long":     strings.Repeat("a", 49),
	}
	for label, name := range cases {
		t.Run(label, func(t *testing.T) {
			s := validSucuriSpec()
			s.Name = name
			if err := s.Validate(); err == nil {
				t.Fatalf("expected error for %s (%q)", label, name)
			}
		})
	}
}

func TestValidate_ImageRequired(t *testing.T) {
	s := validSucuriSpec()
	s.Image = ""
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected image-required error, got: %v", err)
	}
}

func TestValidate_ImageMustBeQualified(t *testing.T) {
	s := validSucuriSpec()
	s.Image = "sucuri" // no registry/repo path
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for unqualified image")
	}
}

func TestValidate_SourceRejected(t *testing.T) {
	s := validSucuriSpec()
	s.Source = "./app"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("expected source-rejected error, got: %v", err)
	}
}

func TestValidate_ProjectRequired(t *testing.T) {
	s := validSucuriSpec()
	s.Project = ""
	if err := s.Validate(); err == nil {
		t.Fatal("expected project-required error")
	}
}

func TestValidate_FrameworkPhase1(t *testing.T) {
	// supported
	for _, fw := range []string{"pode", "streamlit", "shiny", "custom"} {
		s := validSucuriSpec()
		s.Framework = fw
		if fw == "custom" {
			s.Port = 9000
			s.Health = "/health"
		}
		if err := s.Validate(); err != nil {
			t.Fatalf("framework %q should be supported, got: %v", fw, err)
		}
	}
	// recognised but unsupported in phase 1
	for _, fw := range []string{"dash", "panel", "voila"} {
		s := validSucuriSpec()
		s.Framework = fw
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), "not yet supported") {
			t.Fatalf("framework %q should be rejected as phase-2, got: %v", fw, err)
		}
	}
	// unknown
	s := validSucuriSpec()
	s.Framework = "bokeh"
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for unknown framework")
	}
}

func TestValidate_CustomRequiresPortHealth(t *testing.T) {
	s := validSucuriSpec()
	s.Framework = "custom"
	s.Port = 0
	s.Health = ""
	if err := s.Validate(); err == nil {
		t.Fatal("expected custom to require port+health")
	}
}

func TestValidate_AccessPhase1(t *testing.T) {
	for _, a := range []string{"cluster", "public"} {
		s := validSucuriSpec()
		s.Access = a
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), "not yet supported") {
			t.Fatalf("access %q should be rejected in phase 1, got: %v", a, err)
		}
	}
}

func TestValidate_ReplicasPhase1(t *testing.T) {
	s := validSucuriSpec()
	s.Replicas = 2
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("replicas>1 should be rejected in phase 1, got: %v", err)
	}
}

func TestValidate_DataPathRejected(t *testing.T) {
	s := validSucuriSpec()
	s.Data = []DataMount{{Bucket: "b", Path: "/mnt/b"}}
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("expected data path rejection, got: %v", err)
	}
}

func TestValidate_DataAccessValues(t *testing.T) {
	s := validSucuriSpec()
	s.Data = []DataMount{{Bucket: "b", Access: "write"}}
	if err := s.Validate(); err == nil {
		t.Fatal("expected invalid data access to be rejected")
	}
	s.Data = []DataMount{{Bucket: "b", Access: "read-write"}}
	if err := s.Validate(); err != nil {
		t.Fatalf("read-write should be valid, got: %v", err)
	}
}

func TestApplyDefaults_FrameworkTable(t *testing.T) {
	cases := []struct {
		fw         string
		wantPort   int
		wantHealth string
		stateful   bool
	}{
		{"streamlit", 8501, "/_stcore/health", true},
		{"shiny", 3838, "/", true},
		{"pode", 8085, "/health/live", false},
	}
	for _, c := range cases {
		t.Run(c.fw, func(t *testing.T) {
			s := &Spec{Name: "a", Image: "ghcr.io/o/a:1", Project: "p", Framework: c.fw}
			if err := s.Validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			s.ApplyDefaults()
			if s.Port != c.wantPort {
				t.Errorf("port: got %d want %d", s.Port, c.wantPort)
			}
			if s.Health != c.wantHealth {
				t.Errorf("health: got %q want %q", s.Health, c.wantHealth)
			}
			if s.Stateful() != c.stateful {
				t.Errorf("stateful: got %v want %v", s.Stateful(), c.stateful)
			}
			if s.Resources.CPU != defaultCPU || s.Resources.Memory != defaultMemory {
				t.Errorf("resource defaults: got cpu=%d mem=%d", s.Resources.CPU, s.Resources.Memory)
			}
			if s.Replicas != 1 {
				t.Errorf("replicas default: got %d want 1", s.Replicas)
			}
		})
	}
}

func TestDerivedNames(t *testing.T) {
	s := &Spec{Name: "tb-resistance-dashboard", Project: "mtb-resistotyper-ml"}
	if got, want := s.JobName(), "app-mtb-resistotyper-ml-tb-resistance-dashboard"; got != want {
		t.Errorf("JobName: got %q want %q", got, want)
	}
	wantHost := "mtb-resistotyper-ml-tb-resistance-dashboard.apps.seedling.abc-cluster.cloud"
	if got := s.Host(); got != wantHost {
		t.Errorf("Host: got %q want %q", got, wantHost)
	}
	if got, want := s.URL(), "https://"+wantHost; got != want {
		t.Errorf("URL: got %q want %q", got, want)
	}
	if got, want := s.ServiceAccountName(), "abc-app-mtb-resistotyper-ml-tb-resistance-dashboard"; got != want {
		t.Errorf("ServiceAccountName: got %q want %q", got, want)
	}
}

func TestParse_StrictUnknownKey(t *testing.T) {
	_, err := Parse([]byte("name: a\nimage: ghcr.io/o/a:1\nnonsense: x\n"))
	if err == nil {
		t.Fatal("expected strict-decode error for unknown key")
	}
}

func TestExposure_ValidationAndDefault(t *testing.T) {
	// valid values (incl. empty) pass; default resolves to public.
	for _, v := range []string{"", "public", "internal", "both", "INTERNAL", " Both "} {
		s := validSucuriSpec()
		s.Exposure = v
		if err := s.Validate(); err != nil {
			t.Errorf("exposure %q should be valid: %v", v, err)
		}
	}
	// invalid value rejected.
	s := validSucuriSpec()
	s.Exposure = "lan"
	if err := s.Validate(); err == nil {
		t.Errorf("exposure %q should be rejected", s.Exposure)
	}
	// default after ApplyDefaults: planes normalise to [public] (back-compat default).
	d := validSucuriSpec()
	d.ApplyDefaults()
	if got := d.Planes(); len(got) != 1 || got[0] != ExposePublic {
		t.Errorf("default planes = %v, want [public]", got)
	}
}

func TestExposure_Hosts(t *testing.T) {
	pub := "abc-platform-sucuri." + AppsDomain
	internal := "abc-platform-sucuri." + InternalAppsDomain
	cases := []struct {
		exposure  string
		wantHosts []string
		wantHost  string
		wantURL   string
	}{
		{"public", []string{pub}, pub, "https://" + pub},
		{"", []string{pub}, pub, "https://" + pub}, // default
		{"internal", []string{internal}, internal, "/apps/abc-platform-sucuri/"}, // path-routed now
		{"both", []string{pub, internal}, pub, "https://" + pub},
	}
	for _, c := range cases {
		t.Run(c.exposure, func(t *testing.T) {
			s := validSucuriSpec()
			s.Exposure = c.exposure
			s.ApplyDefaults()
			got := s.Hosts()
			if len(got) != len(c.wantHosts) {
				t.Fatalf("Hosts()=%v, want %v", got, c.wantHosts)
			}
			for i := range got {
				if got[i] != c.wantHosts[i] {
					t.Errorf("Hosts()[%d]=%q, want %q", i, got[i], c.wantHosts[i])
				}
			}
			if s.Host() != c.wantHost {
				t.Errorf("Host()=%q, want %q", s.Host(), c.wantHost)
			}
			if s.URL() != c.wantURL {
				t.Errorf("URL()=%q, want %q", s.URL(), c.wantURL)
			}
		})
	}
}

func TestVersion_ValidationAndDefault(t *testing.T) {
	// accepted: empty, legacy "1", current "1.0"
	for _, v := range []string{"", "1", "1.0", " 1.0 "} {
		s := validSucuriSpec()
		s.Version = v
		if err := s.Validate(); err != nil {
			t.Errorf("version %q should be valid: %v", v, err)
		}
	}
	// rejected: a different/future version
	s := validSucuriSpec()
	s.Version = "2.0"
	if err := s.Validate(); err == nil {
		t.Errorf("version %q should be rejected by a v%s CLI", s.Version, CurrentSpecVersion)
	}
	// empty + legacy "1" normalise to current after ApplyDefaults
	for _, v := range []string{"", "1"} {
		d := validSucuriSpec()
		d.Version = v
		d.ApplyDefaults()
		if d.Version != CurrentSpecVersion {
			t.Errorf("version %q normalised to %q, want %q", v, d.Version, CurrentSpecVersion)
		}
	}
}
