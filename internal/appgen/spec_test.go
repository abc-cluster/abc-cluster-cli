package appgen

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	if got, want := s.URL(AppsDoors{}), "https://"+wantHost; got != want {
		t.Errorf("URL: got %q want %q", got, want)
	}
	// Deterministic (same Project+Name -> same key every time) and within
	// MinIO's 3-20 char access-key length limit.
	saName := s.ServiceAccountName()
	if !strings.HasPrefix(saName, "sa-") || len(saName) != 19 {
		t.Errorf("ServiceAccountName: got %q, want \"sa-\" + 16 hex chars (19 total)", saName)
	}
	if got := s.ServiceAccountName(); got != saName {
		t.Errorf("ServiceAccountName: not deterministic, got %q then %q", saName, got)
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
			// URL() now needs per-deployment doors; the test expects bare-path
			// fallback for `internal`, full https for `public` / `both` (uses the
			// build-time AppsDomain const when doors.PublicDomain is empty).
			if got := s.URL(AppsDoors{}); got != c.wantURL {
				t.Errorf("URL()=%q, want %q", got, c.wantURL)
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

// TestExposePlanes_YAMLForms locks the two accepted shapes of the `expose:`
// key — a scalar string for a single plane, and a sequence (flow or block)
// for multiple. Both round-trip through Parse → s.Expose → s.Planes().
func TestExposePlanes_YAMLForms(t *testing.T) {
	baseDoc := func(exposeFragment string) string {
		return `version: "1.0"
name: test-app
image: aither.local/test:latest
project: abc-platform
framework: custom
port: 8080
health: /
access: team
` + exposeFragment
	}

	cases := []struct {
		name     string
		fragment string
		want     []string // canonical (normalised) plane order
	}{
		{"scalar private",       "expose: private",                   []string{ExposePrivate}},
		{"scalar shared",        "expose: shared",                    []string{ExposeShared}},
		{"scalar public",        "expose: public",                    []string{ExposePublic}},
		{"scalar with quotes",   "expose: \"private\"",               []string{ExposePrivate}},
		{"flow sequence two",    "expose: [private, shared]",         []string{ExposeShared, ExposePrivate}},
		{"flow sequence one",    "expose: [private]",                 []string{ExposePrivate}},
		{"flow sequence three",  "expose: [public, shared, private]", []string{ExposePublic, ExposeShared, ExposePrivate}},
		{"block sequence",       "expose:\n  - public\n  - shared",   []string{ExposePublic, ExposeShared}},
		// empty scalar → field is cleared by UnmarshalYAML; ApplyDefaults then
		// applies the "default to public" rule (no expose AND no exposure).
		{"empty scalar defaults", "expose: \"\"",                     []string{ExposePublic}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := Parse([]byte(baseDoc(c.fragment)))
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", c.fragment, err)
			}
			if err := s.Validate(); err != nil {
				t.Fatalf("Validate after parse(%q) error: %v", c.fragment, err)
			}
			s.ApplyDefaults()
			got := s.Planes()
			if !equalStrings(got, c.want) {
				t.Errorf("Planes() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestExposePlanes_InvalidYAMLShape ensures we reject things that are clearly
// neither a scalar nor a sequence (e.g. a mapping). The error message must
// guide the user toward the two supported forms.
func TestExposePlanes_InvalidYAMLShape(t *testing.T) {
	docs := []string{
		// mapping (not allowed)
		`version: "1.0"
name: test-app
image: aither.local/test:latest
project: abc-platform
framework: custom
port: 8080
health: /
access: team
expose:
  private: true
  shared: true
`,
	}
	for i, d := range docs {
		_, err := Parse([]byte(d))
		if err == nil {
			t.Errorf("case %d: expected parse error for mapping-shaped expose, got nil", i)
		}
	}
}

// TestExposePlanes_Marshal locks the fallback yaml.Marshal output:
//   - one plane          → scalar (no list markers)
//   - two or more planes → a sequence (yaml.v3 default style is block; the
//     canonical writer in spec_yaml_ordered.go uses flow style for the
//     full-spec serialization. Both are valid YAML and round-trip through
//     our UnmarshalYAML.)
func TestExposePlanes_Marshal(t *testing.T) {
	cases := []struct {
		name           string
		in             ExposePlanes
		wantContains   []string // substrings expected in output
		wantNotContain []string
	}{
		{"single scalar", ExposePlanes{ExposePrivate},
			[]string{"private"}, []string{"- ", "[", "]"}},
		{"two as sequence", ExposePlanes{ExposePrivate, ExposeShared},
			[]string{"private", "shared"}, nil},
		{"nil empty", nil, []string{"null"}, []string{"private", "shared", "public"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := yaml.Marshal(c.in)
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}
			s := string(out)
			for _, w := range c.wantContains {
				if !strings.Contains(s, w) {
					t.Errorf("Marshal output %q missing %q", s, w)
				}
			}
			for _, n := range c.wantNotContain {
				if strings.Contains(s, n) {
					t.Errorf("Marshal output %q must not contain %q", s, n)
				}
			}
		})
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

// TestURL_WithDoors locks the context-driven URL composition. NOTE: doors
// values here are fixture/illustrative — they are NOT package consts and
// every operator supplies their own via admin.services.apps in the active
// context.
func TestURL_WithDoors(t *testing.T) {
	doors := AppsDoors{
		PublicDomain:  "apps.example.com",
		PrivateDoor:   "lan.example.org",
		PrivateDoorIP: "10.0.0.1",
	}
	cases := []struct {
		name   string
		expose ExposePlanes
		want   string
	}{
		{"public uses doors.PublicDomain",
			ExposePlanes{ExposePublic}, "https://abc-platform-sucuri.apps.example.com"},
		{"private uses doors.PrivateDoor",
			ExposePlanes{ExposePrivate}, "https://lan.example.org/apps/abc-platform-sucuri/"},
		{"shared falls back to PrivateDoor when SharedDoor empty",
			ExposePlanes{ExposeShared}, "https://lan.example.org/apps/abc-platform-sucuri/"},
		{"public+private — public wins",
			ExposePlanes{ExposePublic, ExposePrivate}, "https://abc-platform-sucuri.apps.example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := validSucuriSpec()
			s.Expose = c.expose
			s.Exposure = ""
			s.ApplyDefaults()
			if got := s.URL(doors); got != c.want {
				t.Errorf("URL(doors) = %q, want %q", got, c.want)
			}
		})
	}
}

// TestURL_EmptyDoors falls through cleanly when the operator hasn't configured
// the relevant door (the open-source default for non-seedling deployments).
// Public still works via the build-time AppsDomain fallback for back-compat.
func TestURL_EmptyDoors(t *testing.T) {
	cases := []struct {
		name   string
		expose ExposePlanes
		want   string
	}{
		{"public still uses build-time const fallback (back-compat)",
			ExposePlanes{ExposePublic}, "https://abc-platform-sucuri." + AppsDomain},
		{"private with no door → bare path",
			ExposePlanes{ExposePrivate}, "/apps/abc-platform-sucuri/"},
		{"shared with no door → bare path",
			ExposePlanes{ExposeShared}, "/apps/abc-platform-sucuri/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := validSucuriSpec()
			s.Expose = c.expose
			s.Exposure = ""
			s.ApplyDefaults()
			if got := s.URL(AppsDoors{}); got != c.want {
				t.Errorf("URL(empty) = %q, want %q", got, c.want)
			}
		})
	}
}

// TestURLIP locks the IP-form composition for private/shared apps.
func TestURLIP(t *testing.T) {
	doors := AppsDoors{
		PrivateDoor:   "lan.example.org",
		PrivateDoorIP: "10.0.0.1",
		SharedDoor:    "tail.example.ts.net",
		SharedDoorIP:  "100.64.0.1",
	}
	cases := []struct {
		name   string
		expose ExposePlanes
		want   string
	}{
		{"public only — no IP URL",
			ExposePlanes{ExposePublic}, ""},
		{"private — PrivateDoorIP",
			ExposePlanes{ExposePrivate}, "https://10.0.0.1/apps/abc-platform-sucuri/"},
		{"shared — SharedDoorIP",
			ExposePlanes{ExposeShared}, "https://100.64.0.1/apps/abc-platform-sucuri/"},
		{"private + shared — PrivateDoorIP (private wins)",
			ExposePlanes{ExposePrivate, ExposeShared}, "https://10.0.0.1/apps/abc-platform-sucuri/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := validSucuriSpec()
			s.Expose = c.expose
			s.Exposure = ""
			s.ApplyDefaults()
			if got := s.URLIP(doors); got != c.want {
				t.Errorf("URLIP(doors) = %q, want %q", got, c.want)
			}
		})
	}
}

// TestURLIP_EmptyDoors: no IP URL when operator hasn't supplied one.
func TestURLIP_EmptyDoors(t *testing.T) {
	s := validSucuriSpec()
	s.Expose = ExposePlanes{ExposePrivate}
	s.Exposure = ""
	s.ApplyDefaults()
	if got := s.URLIP(AppsDoors{}); got != "" {
		t.Errorf("URLIP(empty) = %q, want empty", got)
	}
}
