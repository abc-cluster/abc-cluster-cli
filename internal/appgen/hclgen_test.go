package appgen

import (
	"regexp"
	"strings"
	"testing"
)

// spaceRe collapses runs of spaces so assertions are HCL-alignment-agnostic
// (hclwrite aligns `=` to the longest key in a block, which shifts per test).
var spaceRe = regexp.MustCompile(`[ ]+`)

func norm(s string) string { return spaceRe.ReplaceAllString(s, " ") }

// containsNorm reports whether haystack contains frag, ignoring run-length of
// spaces on both sides.
func containsNorm(haystack, frag string) bool {
	return strings.Contains(norm(haystack), norm(frag))
}

func resolvedSucuri(t *testing.T) *Spec {
	t.Helper()
	s := validSucuriSpec()
	if err := s.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	s.ApplyDefaults()
	return s
}

func TestGenerate_ServiceJobShape(t *testing.T) {
	s := resolvedSucuri(t)
	hcl := Generate(s, JobParams{
		Namespace:     "abc-apps",
		Datacenters:   []string{"dc1"},
		NodePool:      "platform",
		MinIOEndpoint: "https://minio.seedling:9000",
	})

	must := []string{
		`job "app-abc-platform-sucuri"`,
		`namespace = "abc-apps"`,
		`type      = "service"`,
		`node_pool   = "platform"`,
		`provider = "nomad"`, // Nomad-native, not Consul
		`name     = "app-abc-platform-sucuri"`,
		"traefik.enable=true",
		"Host(`abc-platform-sucuri.apps.seedling.abc-cluster.cloud`)",
		"to = 8085", // bridge: dynamic host port -> container port (no static host port)
		`abc_project = "abc-platform"`,
		`image = "ghcr.io/biosharp-dotnet/sucuri-api:latest"`,
		`count = 1`,
	}
	for _, frag := range must {
		if !containsNorm(hcl, frag) {
			t.Errorf("generated HCL missing fragment:\n  %s\n--- HCL ---\n%s", frag, hcl)
		}
	}
	// Bridge networking + dynamic-port discovery: these must NOT appear (they were
	// the host-net + static-port era that collided when apps shared a container port).
	for _, banned := range []string{"loadbalancer.server.port", `mode = "host"`, `network_mode = "host"`} {
		if containsNorm(hcl, banned) {
			t.Errorf("generated HCL must NOT contain %q (bridge networking; Traefik uses the dynamic registered port):\n%s", banned, hcl)
		}
	}
}

func TestGenerate_NoStripPrefixNoMiddleware(t *testing.T) {
	s := resolvedSucuri(t)
	hcl := Generate(s, JobParams{Namespace: "abc-apps"})
	for _, banned := range []string{"stripprefix", "stripPrefix", "middleware", "forwardauth", "abc-auth"} {
		if strings.Contains(strings.ToLower(hcl), strings.ToLower(banned)) {
			t.Errorf("generated HCL must NOT contain %q (auth/strip is at the Caddy edge):\n%s", banned, hcl)
		}
	}
}

func TestGenerate_NoStickyCookiePhase1(t *testing.T) {
	// streamlit is stateful, but phase 1 (single replica) must emit no
	// sticky-cookie / spread / count>1.
	s := &Spec{Name: "dash", Image: "ghcr.io/o/a:1", Project: "p", Framework: "streamlit"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.ApplyDefaults()
	hcl := Generate(s, JobParams{Namespace: "abc-apps"})
	for _, banned := range []string{"sticky", "spread {", "count = 2"} {
		if strings.Contains(hcl, banned) {
			t.Errorf("phase-1 HCL must not contain %q:\n%s", banned, hcl)
		}
	}
}

func TestGenerate_HealthCheckAndPolicies(t *testing.T) {
	s := resolvedSucuri(t)
	hcl := Generate(s, JobParams{Namespace: "abc-apps"})
	must := []string{
		`path     = "/health/ready"`,
		`interval = "10s"`,
		`timeout  = "5s"`,
		// restart policy
		`attempts = 3`,
		`delay    = "15s"`,
		`mode     = "delay"`,
		// update policy
		`max_parallel     = 1`,
		`health_check     = "checks"`,
	}
	for _, frag := range must {
		if !containsNorm(hcl, frag) {
			t.Errorf("missing policy fragment %q in:\n%s", frag, hcl)
		}
	}
}

func TestGenerate_EnvInjectionAndPlatformWins(t *testing.T) {
	s := resolvedSucuri(t)
	// User tries to override a reserved platform key — platform must win.
	s.Env["ABC_APP_NAME"] = "hacked"
	s.Env["ABC_MINIO_ENDPOINT"] = "http://evil"
	hcl := Generate(s, JobParams{
		Namespace:     "abc-apps",
		MinIOEndpoint: "https://minio.seedling:9000",
		AWSAccessKey:  "AKIA_TEST",
		AWSSecretKey:  "secret_test",
	})
	must := []string{
		`ABC_APP_NAME          = "sucuri"`, // platform wins over user "hacked"
		`ABC_APP_BASE_URL      = "/"`,      // always root
		`ABC_PROJECT           = "abc-platform"`,
		`ABC_MINIO_ENDPOINT    = "https://minio.seedling:9000"`,
		`AWS_ACCESS_KEY_ID     = "AKIA_TEST"`,
		`AWS_SECRET_ACCESS_KEY = "secret_test"`,
		`SUCURI_ENVIRONMENT    = "production"`, // user env preserved
	}
	for _, frag := range must {
		if !containsNorm(hcl, frag) {
			t.Errorf("missing env fragment %q in:\n%s", frag, hcl)
		}
	}
	if strings.Contains(hcl, "hacked") || strings.Contains(hcl, "http://evil") {
		t.Errorf("platform env did not win over user override:\n%s", hcl)
	}
	// No base-URL framework arg injected.
	if strings.Contains(hcl, "baseUrlPath") || strings.Contains(hcl, "--base_url") {
		t.Errorf("no framework base-URL arg should be injected:\n%s", hcl)
	}
}

func TestGenerate_NoDataNoAWSEnv(t *testing.T) {
	s := resolvedSucuri(t)
	hcl := Generate(s, JobParams{Namespace: "abc-apps"}) // no MinIO, no creds
	if strings.Contains(hcl, "AWS_ACCESS_KEY_ID") {
		t.Errorf("no-data app should not get AWS_* env:\n%s", hcl)
	}
}

func TestGenerate_MergeEnvPlatformWins(t *testing.T) {
	got := mergeEnv(
		map[string]string{"A": "user", "B": "user"},
		map[string]string{"A": "platform"},
	)
	if got["A"] != "platform" {
		t.Errorf("platform should win: got %q", got["A"])
	}
	if got["B"] != "user" {
		t.Errorf("user-only key should survive: got %q", got["B"])
	}
}

func TestGenerate_BucketsAndStampInMeta(t *testing.T) {
	s := validSucuriSpec()
	s.Data = []DataMount{{Bucket: "mtb-resistotyper-ml", Access: "read"}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.ApplyDefaults()
	hcl := Generate(s, JobParams{Namespace: "abc-apps"})
	if !strings.Contains(hcl, "abc_data_buckets") {
		t.Errorf("expected data buckets stamped in meta:\n%s", hcl)
	}
}

func TestGenerate_Exposure(t *testing.T) {
	pubHost := "Host(`abc-platform-sucuri.apps.seedling.abc-cluster.cloud`)"
	intHost := "Host(`abc-platform-sucuri.apps.internal`)"

	// internal: router rule must use the internal host and NOT the public one.
	si := resolvedSucuriWithExposure(t, "internal")
	hi := Generate(si, JobParams{Namespace: "abc-apps"})
	if !containsNorm(hi, "rule="+intHost) {
		t.Errorf("internal exposure should route to %s:\n%s", intHost, hi)
	}
	if strings.Contains(hi, pubHost) {
		t.Errorf("internal exposure must NOT carry the public-edge host:\n%s", hi)
	}
	if !containsNorm(hi, `abc_exposure = "internal"`) {
		t.Errorf("internal exposure should be stamped in meta:\n%s", hi)
	}

	// both: rule must OR the public and internal hosts.
	sb := resolvedSucuriWithExposure(t, "both")
	hb := Generate(sb, JobParams{Namespace: "abc-apps"})
	if !containsNorm(hb, "rule="+pubHost+" || "+intHost) {
		t.Errorf("both exposure should OR public and internal hosts:\n%s", hb)
	}

	// public (default): unchanged — public host, no internal host.
	sp := resolvedSucuri(t)
	hp := Generate(sp, JobParams{Namespace: "abc-apps"})
	if !containsNorm(hp, "rule="+pubHost) || strings.Contains(hp, intHost) {
		t.Errorf("public exposure should route to the public host only:\n%s", hp)
	}
}

func resolvedSucuriWithExposure(t *testing.T, exposure string) *Spec {
	t.Helper()
	s := validSucuriSpec()
	s.Exposure = exposure
	if err := s.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	s.ApplyDefaults()
	return s
}
