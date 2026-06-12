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

// TestGenerate_NoAuthMiddleware locks the no-auth-middleware decision: forward-auth
// is at the Caddy edge, not in Traefik. StripPrefix is NOW framework-conditional
// (see TestGenerate_StripPrefix_*); the original test name was misleading.
func TestGenerate_NoAuthMiddleware(t *testing.T) {
	s := resolvedSucuri(t) // pode framework → no stripPrefix
	hcl := Generate(s, JobParams{Namespace: "abc-apps"})
	for _, banned := range []string{"forwardauth", "abc-auth"} {
		if strings.Contains(strings.ToLower(hcl), strings.ToLower(banned)) {
			t.Errorf("generated HCL must NOT contain %q (auth is at the Caddy edge):\n%s", banned, hcl)
		}
	}
	// Non-custom framework on public-only exposure: stripPrefix MUST be absent.
	for _, banned := range []string{"stripprefix", "middlewares="} {
		if strings.Contains(strings.ToLower(hcl), strings.ToLower(banned)) {
			t.Errorf("pode (non-custom, public-only) must NOT emit stripPrefix: contained %q\n%s", banned, hcl)
		}
	}
}

// TestGenerate_StripPrefix_CustomPrivateEmitsMiddleware exercises the v0.1.55
// fix: a `framework: custom` app on private/shared planes MUST emit a
// stripPrefix middleware so Traefik strips `/apps/<project>-<name>` before
// forwarding (else the BYOI container serving at `/` returns 404).
func TestGenerate_StripPrefix_CustomPrivateEmitsMiddleware(t *testing.T) {
	s := &Spec{
		Name:      "docs",
		Image:     "aither.local/docs:poc",
		Project:   "abc-platform",
		Framework: "custom",
		Port:      8080,
		Health:    "/",
		Expose:    ExposePlanes{ExposePrivate, ExposeShared},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	s.ApplyDefaults()
	if s.StripPrefix == nil || !*s.StripPrefix {
		t.Fatalf("custom framework: StripPrefix default should be true; got %v", s.StripPrefix)
	}
	hcl := Generate(s, JobParams{Namespace: "abc-apps"})
	must := []string{
		"traefik.http.middlewares.app-abc-platform-docs-strip.stripprefix.prefixes=/apps/abc-platform-docs",
		"traefik.http.routers.app-abc-platform-docs-internal.middlewares=app-abc-platform-docs-strip@nomad-abc-apps",
	}
	for _, w := range must {
		if !strings.Contains(hcl, w) {
			t.Errorf("expected tag %q in HCL:\n%s", w, hcl)
		}
	}
}

// TestGenerate_StripPrefix_StreamlitPrivateNoMiddleware: non-custom frameworks
// serve under the prefix natively (--server.baseUrlPath etc.) and MUST NOT get
// stripPrefix — otherwise the framework would double-handle the prefix.
func TestGenerate_StripPrefix_StreamlitPrivateNoMiddleware(t *testing.T) {
	s := &Spec{
		Name:      "dash",
		Image:     "ghcr.io/o/a:1",
		Project:   "p",
		Framework: "streamlit",
		Expose:    ExposePlanes{ExposePrivate, ExposeShared},
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.ApplyDefaults()
	if s.StripPrefix == nil || *s.StripPrefix {
		t.Errorf("streamlit framework: StripPrefix default should be false; got %v", s.StripPrefix)
	}
	hcl := Generate(s, JobParams{Namespace: "abc-apps"})
	if strings.Contains(strings.ToLower(hcl), "stripprefix") {
		t.Errorf("streamlit (non-custom) must NOT emit stripPrefix:\n%s", hcl)
	}
}

// TestGenerate_StripPrefix_ExplicitOverride: the abc-app.yaml author can pin
// `strip_prefix: <bool>` to override the framework-derived default.
func TestGenerate_StripPrefix_ExplicitOverride(t *testing.T) {
	t.Run("custom + strip_prefix: false → no middleware", func(t *testing.T) {
		s := &Spec{
			Name: "docs", Image: "x/y:z", Project: "p", Framework: "custom",
			Port: 8080, Health: "/",
			Expose: ExposePlanes{ExposePrivate},
		}
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		off := false
		s.StripPrefix = &off
		s.ApplyDefaults() // must NOT clobber the explicit value
		if s.StripPrefix == nil || *s.StripPrefix {
			t.Fatalf("ApplyDefaults clobbered explicit strip_prefix=false: %v", s.StripPrefix)
		}
		hcl := Generate(s, JobParams{Namespace: "abc-apps"})
		if strings.Contains(strings.ToLower(hcl), "stripprefix") {
			t.Errorf("explicit strip_prefix=false must suppress middleware:\n%s", hcl)
		}
	})
	t.Run("streamlit + strip_prefix: true → middleware emitted", func(t *testing.T) {
		s := &Spec{
			Name: "dash", Image: "x/y:z", Project: "p", Framework: "streamlit",
			Expose: ExposePlanes{ExposePrivate},
		}
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		on := true
		s.StripPrefix = &on
		s.ApplyDefaults()
		if s.StripPrefix == nil || !*s.StripPrefix {
			t.Fatalf("ApplyDefaults clobbered explicit strip_prefix=true: %v", s.StripPrefix)
		}
		hcl := Generate(s, JobParams{Namespace: "abc-apps"})
		if !strings.Contains(strings.ToLower(hcl), "stripprefix") {
			t.Errorf("explicit strip_prefix=true must emit middleware:\n%s", hcl)
		}
	})
}

// TestGenerate_StripPrefix_PublicOnlyNoMiddleware: stripPrefix is only relevant
// for path-prefix planes (private/shared). A public-only app uses Host-rule
// routing — no prefix to strip.
func TestGenerate_StripPrefix_PublicOnlyNoMiddleware(t *testing.T) {
	s := &Spec{
		Name: "docs", Image: "x/y:z", Project: "p", Framework: "custom",
		Port: 8080, Health: "/",
		Expose: ExposePlanes{ExposePublic},
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.ApplyDefaults()
	hcl := Generate(s, JobParams{Namespace: "abc-apps"})
	if strings.Contains(strings.ToLower(hcl), "stripprefix") {
		t.Errorf("public-only exposure must NOT emit stripPrefix (no prefix):\n%s", hcl)
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
	pubRule := "rule=Host(`abc-platform-sucuri.apps.seedling.abc-cluster.cloud`)"
	privPath := "rule=PathPrefix(`/apps/abc-platform-sucuri`)"

	// legacy `exposure: internal` → maps to [shared, private]: one PathPrefix router
	// on both internal entrypoints; NO public host.
	si := resolvedSucuriWithExposure(t, "internal")
	hi := Generate(si, JobParams{Namespace: "abc-apps"})
	if !containsNorm(hi, privPath) {
		t.Errorf("internal exposure should route by PathPrefix(/apps/<app>):\n%s", hi)
	}
	if !containsNorm(hi, "app-abc-platform-sucuri-internal.entrypoints=private,shared") {
		t.Errorf("internal exposure should bind the private+shared entrypoints:\n%s", hi)
	}
	if strings.Contains(hi, "apps.seedling.abc-cluster.cloud") {
		t.Errorf("internal exposure must NOT carry the public-edge host:\n%s", hi)
	}

	// legacy `both` → public Host router + internal PathPrefix router.
	sb := resolvedSucuriWithExposure(t, "both")
	hb := Generate(sb, JobParams{Namespace: "abc-apps"})
	if !containsNorm(hb, pubRule) || !containsNorm(hb, privPath) {
		t.Errorf("both exposure should emit a public Host router AND an internal PathPrefix router:\n%s", hb)
	}

	// public (default): public Host router only, no PathPrefix router.
	sp := resolvedSucuri(t)
	hp := Generate(sp, JobParams{Namespace: "abc-apps"})
	if !containsNorm(hp, pubRule) || strings.Contains(hp, "PathPrefix") {
		t.Errorf("public exposure should route by Host only (no PathPrefix):\n%s", hp)
	}

	// new `expose: [private]` → PathPrefix on the private entrypoint ONLY (not shared,
	// not public). The app name is the SAME as the public subdomain.
	spr := resolvedSucuriWithExpose(t, []string{"private"})
	hpr := Generate(spr, JobParams{Namespace: "abc-apps"})
	if !containsNorm(hpr, privPath) ||
		!containsNorm(hpr, "app-abc-platform-sucuri-internal.entrypoints=private") {
		t.Errorf("expose:[private] should route PathPrefix(/apps/<app>) on entrypoint private:\n%s", hpr)
	}
	if strings.Contains(hpr, "entrypoints=private,shared") || strings.Contains(hpr, "apps.seedling.abc-cluster.cloud") {
		t.Errorf("expose:[private] must NOT bind shared or the public host:\n%s", hpr)
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

func resolvedSucuriWithExpose(t *testing.T, expose []string) *Spec {
	t.Helper()
	s := validSucuriSpec()
	s.Expose = expose
	if err := s.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	s.ApplyDefaults()
	return s
}

// TestGenerate_MetaURLs_PrivateAppWithDoors: v0.1.57 meta block emits both
// abc_url (full DNS URL) and abc_url_ip (bare-IP form) for private/shared
// apps when the supplied JobParams.AppsDoors carries them.
func TestGenerate_MetaURLs_PrivateAppWithDoors(t *testing.T) {
	doors := AppsDoors{
		PrivateDoor:   "lan.example.org",
		PrivateDoorIP: "10.0.0.1",
	}
	s := &Spec{
		Name: "docs", Image: "x/y:z", Project: "abc-platform", Framework: "custom",
		Port: 8080, Health: "/",
		Expose: ExposePlanes{ExposePrivate, ExposeShared},
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.ApplyDefaults()
	hcl := Generate(s, JobParams{Namespace: "abc-apps", AppsDoors: doors})
	wantDNS := `abc_url       = "https://lan.example.org/apps/abc-platform-docs/"`
	wantIP := `abc_url_ip    = "https://10.0.0.1/apps/abc-platform-docs/"`
	if !strings.Contains(hcl, wantDNS) {
		t.Errorf("expected meta abc_url DNS form %q:\n%s", wantDNS, hcl)
	}
	if !strings.Contains(hcl, wantIP) {
		t.Errorf("expected meta abc_url_ip %q:\n%s", wantIP, hcl)
	}
}

// TestGenerate_MetaURLs_PublicAppNoIP: public-only apps don't emit abc_url_ip.
func TestGenerate_MetaURLs_PublicAppNoIP(t *testing.T) {
	s := resolvedSucuri(t) // public default
	hcl := Generate(s, JobParams{Namespace: "abc-apps"})
	wantPub := `abc_url       = "https://abc-platform-sucuri.` + AppsDomain + `"`
	if !strings.Contains(hcl, wantPub) {
		t.Errorf("expected public abc_url:\n%s", hcl)
	}
	if strings.Contains(hcl, "abc_url_ip") {
		t.Errorf("public-only app must NOT emit abc_url_ip:\n%s", hcl)
	}
}

// TestGenerate_MetaURLs_PrivateAppNoDoorsBareFallback: when the operator has
// not yet configured admin.services.apps in their context, the meta abc_url
// falls back to the bare path (and abc_url_ip is omitted).
func TestGenerate_MetaURLs_PrivateAppNoDoorsBareFallback(t *testing.T) {
	s := &Spec{
		Name: "docs", Image: "x/y:z", Project: "abc-platform", Framework: "custom",
		Port: 8080, Health: "/",
		Expose: ExposePlanes{ExposePrivate},
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.ApplyDefaults()
	hcl := Generate(s, JobParams{Namespace: "abc-apps"}) // empty AppsDoors
	want := `abc_url       = "/apps/abc-platform-docs/"`
	if !strings.Contains(hcl, want) {
		t.Errorf("expected bare-path abc_url fallback %q:\n%s", want, hcl)
	}
	if strings.Contains(hcl, "abc_url_ip") {
		t.Errorf("no doors configured → must NOT emit abc_url_ip:\n%s", hcl)
	}
}
