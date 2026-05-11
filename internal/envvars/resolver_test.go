package envvars

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func flagMap(m map[string]string) FlagLookup {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

func ctxMap(m map[string]string) ContextLookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func newTestResolver(flags, env, ctx map[string]string, hasABCContext bool) (*Resolver, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &Resolver{
		Flag:          flagMap(flags),
		Env:           MapEnv(env),
		Context:       ctxMap(ctx),
		WarnSink:      buf,
		HasABCContext: hasABCContext,
	}, buf
}

// ── Precedence: flag > ABC env > vendor env > context > default ─────────

func TestResolve_FlagBeatsEverything(t *testing.T) {
	r, _ := newTestResolver(
		map[string]string{"address": "from-flag"},
		map[string]string{"ABC_API_ADDR": "from-env"},
		map[string]string{"url": "from-context"},
		true,
	)
	v, src, err := r.Resolve("ABC_API_ADDR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "from-flag" || src != SourceFlag {
		t.Errorf("got (%q, %v); want (from-flag, flag)", v, src)
	}
}

func TestResolve_ABCEnvBeatsVendorEnv(t *testing.T) {
	r, _ := newTestResolver(
		nil,
		map[string]string{"ABC_REGION": "from-abc", "NOMAD_REGION": "from-nomad"},
		map[string]string{"region": "from-context"},
		true,
	)
	v, src, err := r.Resolve("ABC_REGION")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "from-abc" || src != SourceABCEnv {
		t.Errorf("got (%q, %v); want (from-abc, abc-env)", v, src)
	}
}

func TestResolve_ExplicitEmptyABCEnvWins(t *testing.T) {
	// Critical: explicit ABC_API_ADDR= must beat context, not fall through.
	r, _ := newTestResolver(
		nil,
		map[string]string{"ABC_API_ADDR": ""},
		map[string]string{"url": "from-context"},
		true,
	)
	v, src, err := r.Resolve("ABC_API_ADDR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "" || src != SourceABCEnv {
		t.Errorf("got (%q, %v); want (\"\", abc-env)", v, src)
	}
}

func TestResolve_VendorFallbackUsed(t *testing.T) {
	r, _ := newTestResolver(
		nil,
		map[string]string{"NOMAD_REGION": "za-cpt"},
		nil,
		true,
	)
	v, src, err := r.Resolve("ABC_REGION")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "za-cpt" || src != SourceVendorEnv {
		t.Errorf("got (%q, %v); want (za-cpt, vendor-env)", v, src)
	}
}

func TestResolve_VendorFallbackWarnsWhenNoABCContext(t *testing.T) {
	r, warn := newTestResolver(
		nil,
		map[string]string{"NOMAD_REGION": "za-cpt"},
		nil,
		false,
	)
	_, _, _ = r.Resolve("ABC_REGION")
	if !strings.Contains(warn.String(), "using NOMAD_REGION") {
		t.Errorf("expected vendor-fallback warning, got: %q", warn.String())
	}
}

func TestResolve_VendorFallbackSilentWhenABCContextExists(t *testing.T) {
	r, warn := newTestResolver(
		nil,
		map[string]string{"NOMAD_REGION": "za-cpt"},
		nil,
		true,
	)
	_, _, _ = r.Resolve("ABC_REGION")
	if warn.Len() != 0 {
		t.Errorf("expected silent vendor fallback, got: %q", warn.String())
	}
}

func TestResolve_ContextWhenNoEnv(t *testing.T) {
	r, _ := newTestResolver(
		nil,
		nil,
		map[string]string{"url": "from-context"},
		true,
	)
	v, src, err := r.Resolve("ABC_API_ADDR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "from-context" || src != SourceContext {
		t.Errorf("got (%q, %v); want (from-context, context)", v, src)
	}
}

func TestResolve_DefaultWhenNothingElse(t *testing.T) {
	r, _ := newTestResolver(nil, nil, nil, true)
	v, src, err := r.Resolve("ABC_CLI_OUTPUT_FORMAT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "table" || src != SourceDefault {
		t.Errorf("got (%q, %v); want (table, default)", v, src)
	}
}

func TestResolve_UnsetReturnsSourceUnset(t *testing.T) {
	r, _ := newTestResolver(nil, nil, nil, true)
	v, src, err := r.Resolve("ABC_API_ADDR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "" || src != SourceUnset {
		t.Errorf("got (%q, %v); want (\"\", unset)", v, src)
	}
}

func TestResolve_UnknownNameErrors(t *testing.T) {
	r, _ := newTestResolver(nil, nil, nil, true)
	_, _, err := r.Resolve("ABC_BOGUS_DOES_NOT_EXIST")
	if err == nil {
		t.Fatalf("expected error for unknown name")
	}
}

// ── Registry sanity ─────────────────────────────────────────────────────

func TestRegistry_NoDuplicateCanonical(t *testing.T) {
	seen := map[string]struct{}{}
	for _, e := range Registry {
		if _, dup := seen[e.Name]; dup {
			t.Errorf("duplicate canonical entry: %q", e.Name)
		}
		seen[e.Name] = struct{}{}
	}
}

func TestRegistry_NoForbiddenPatterns(t *testing.T) {
	for _, e := range Registry {
		n := e.Name
		if strings.HasPrefix(n, "ABC_DISABLE_") {
			t.Errorf("forbidden: %q (use ABC_<SCOPE>_NO_*)", n)
		}
		if strings.HasSuffix(n, "_OFF") && strings.HasPrefix(n, "ABC_") {
			t.Errorf("forbidden: %q (use ABC_<SCOPE>_NO_*)", n)
		}
		if strings.HasPrefix(n, "ABC_GROVE_") ||
			strings.HasPrefix(n, "ABC_SEEDLING_") ||
			strings.HasPrefix(n, "ABC_CLOUD_") {
			t.Errorf("forbidden: %q (tier-coupled — commandment 6)", n)
		}
	}
}

func TestRegistry_LookupCanonical(t *testing.T) {
	e, ok := Lookup("ABC_API_ADDR")
	if !ok || e.Name != "ABC_API_ADDR" {
		t.Errorf("Lookup(ABC_API_ADDR) = (%v, %v); want canonical entry", e, ok)
	}
	// Spot-check: a name that was never in the registry returns false.
	_, ok = Lookup("ABC_NOT_A_REAL_NAME")
	if ok {
		t.Error("Lookup should return false for unknown names")
	}
}

func TestRegistry_AllNamesProperlyScoped(t *testing.T) {
	resourceSelectors := map[string]bool{
		"ABC_WORKSPACE":     true,
		"ABC_REGION":        true,
		"ABC_NAMESPACE":     true,
		"ABC_ORG":           true,
		"ABC_CLUSTER":       true,
		"ABC_PROJECT":       true,
		"ABC_INVESTIGATION": true,
	}
	for _, e := range Registry {
		n := e.Name
		if !strings.HasPrefix(n, "ABC_") {
			// Vendor entries (NOMAD_*, VAULT_*, AWS_*) are allowed.
			continue
		}
		// Resource selectors are allowed in plain ABC_<RESOURCE> form.
		if resourceSelectors[n] {
			continue
		}
		// Everything else MUST have a scope segment.
		parts := strings.SplitN(n, "_", 3)
		if len(parts) < 3 {
			t.Errorf("name %q lacks scope prefix (want ABC_<SCOPE>_<PROPERTY>)", n)
		}
	}
}

// ── Subprocess injection ────────────────────────────────────────────────

func TestInjectVendor_NomadSetsAllFour(t *testing.T) {
	cmd := stubCmd()
	cmd.Env = []string{"PATH=/usr/bin"}
	InjectVendor(cmd, ToolNomad, Resolved{
		NomadAddr:      "http://nomad:4646",
		NomadToken:     "tok",
		NomadRegion:    "global",
		NomadNamespace: "default",
	}, SudoOptOuts{})

	wantKeys := []string{"NOMAD_ADDR", "NOMAD_TOKEN", "NOMAD_REGION", "NOMAD_NAMESPACE"}
	for _, k := range wantKeys {
		if !envContains(cmd.Env, k) {
			t.Errorf("missing %s in cmd.Env: %v", k, cmd.Env)
		}
	}
	if !envContains(cmd.Env, "PATH") {
		t.Errorf("pre-existing PATH lost: %v", cmd.Env)
	}
}

func TestInjectVendor_EmptyValuesSkipped(t *testing.T) {
	cmd := stubCmd()
	cmd.Env = []string{}
	InjectVendor(cmd, ToolNomad, Resolved{NomadAddr: "x"}, SudoOptOuts{})
	if envContains(cmd.Env, "NOMAD_TOKEN") {
		t.Errorf("empty NomadToken should not produce env entry: %v", cmd.Env)
	}
	if !envContains(cmd.Env, "NOMAD_ADDR") {
		t.Errorf("NOMAD_ADDR missing: %v", cmd.Env)
	}
}

func TestInjectVendor_OptOutPreservesParentValue(t *testing.T) {
	cmd := stubCmd()
	cmd.Env = []string{"PATH=/usr/bin", "NOMAD_ADDR=parent-value"}
	InjectVendor(cmd, ToolNomad, Resolved{
		NomadAddr: "abc-derived-value",
	}, SudoOptOuts{NomadAddr: true})

	count := 0
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "NOMAD_ADDR=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one NOMAD_ADDR entry, got %d: %v", count, cmd.Env)
	}
	if !envEquals(cmd.Env, "NOMAD_ADDR", "parent-value") {
		t.Errorf("opt-out should preserve parent value, got: %v", cmd.Env)
	}
}

func TestSudoOptOutsFromEnv_ReadsAllFlags(t *testing.T) {
	env := MapEnv(map[string]string{
		"ABC_CLI_NO_DERIVE_NOMAD_ADDR":  "1",
		"ABC_CLI_NO_DERIVE_VAULT_TOKEN": "true",
		"ABC_CLI_NO_DERIVE_AWS_REGION":  "yes",
	})
	opts := SudoOptOutsFromEnv(env)
	if !opts.NomadAddr {
		t.Error("NomadAddr opt-out not detected from =1")
	}
	if !opts.VaultToken {
		t.Error("VaultToken opt-out not detected from =true")
	}
	if !opts.AWSRegion {
		t.Error("AWSRegion opt-out not detected from =yes")
	}
	if opts.NomadToken {
		t.Error("NomadToken opt-out spuriously set")
	}
}

// ── Storage-extended subprocess injection ───────────────────────────────

func TestInjectVendor_MCWritesHostAlias(t *testing.T) {
	cmd := stubCmd()
	cmd.Env = []string{}
	InjectVendor(cmd, ToolMC, Resolved{
		MCHostAlias: "ceri-grove",
		MCHostURL:   "https://user:pass@s3.ceri.za",
		MCInsecure:  true,
	}, SudoOptOuts{})
	if !envEquals(cmd.Env, "MC_HOST_ceri-grove", "https://user:pass@s3.ceri.za") {
		t.Errorf("MC_HOST_ceri-grove missing or wrong: %v", cmd.Env)
	}
	if !envEquals(cmd.Env, "MC_INSECURE", "1") {
		t.Errorf("MC_INSECURE not set to 1: %v", cmd.Env)
	}
}

func TestInjectVendor_MCDefaultAliasIsLocal(t *testing.T) {
	cmd := stubCmd()
	cmd.Env = []string{}
	InjectVendor(cmd, ToolMC, Resolved{MCHostURL: "http://u:p@host"}, SudoOptOuts{})
	if !envContains(cmd.Env, "MC_HOST_local") {
		t.Errorf("empty MCHostAlias should default to 'local': %v", cmd.Env)
	}
}

func TestInjectVendor_PulumiWritesMinIOTriad(t *testing.T) {
	cmd := stubCmd()
	cmd.Env = []string{}
	InjectVendor(cmd, ToolPulumi, Resolved{
		MinIOServer:   "minio.ceri.za:9000",
		MinIOUser:     "admin",
		MinIOPassword: "s3cret",
		NomadAddr:     "http://nomad.internal:4646",
	}, SudoOptOuts{})
	for _, k := range []string{"MINIO_SERVER", "MINIO_USER", "MINIO_PASSWORD", "NOMAD_ADDR"} {
		if !envContains(cmd.Env, k) {
			t.Errorf("ToolPulumi should set %s: %v", k, cmd.Env)
		}
	}
	// Pulumi MinIO provider does NOT use AWS_*; absence is the contract.
	if envContains(cmd.Env, "AWS_ACCESS_KEY_ID") {
		t.Errorf("ToolPulumi should not set AWS_* (Pulumi MinIO provider uses MINIO_*): %v", cmd.Env)
	}
}

func TestInjectVendor_RcloneWritesConfigPath(t *testing.T) {
	cmd := stubCmd()
	cmd.Env = []string{}
	InjectVendor(cmd, ToolRclone, Resolved{
		RcloneConfig:   "/tmp/abc-rclone-xxx.conf",
		AWSAccessKeyID: "k",
	}, SudoOptOuts{})
	if !envEquals(cmd.Env, "RCLONE_CONFIG", "/tmp/abc-rclone-xxx.conf") {
		t.Errorf("RCLONE_CONFIG missing: %v", cmd.Env)
	}
}

func TestInjectVendor_AWSExtendedFamily(t *testing.T) {
	cmd := stubCmd()
	cmd.Env = []string{}
	InjectVendor(cmd, ToolRclone, Resolved{
		AWSAccessKeyID:                "k",
		AWSSecretAccessKey:            "s",
		AWSEndpointURL:                "http://minio:9000",
		AWSRegion:                     "us-east-1",
		AWSDefaultRegion:              "us-east-1",
		AWSSessionToken:               "sts-tok",
		AWSCABundle:                   "/etc/ssl/abc.pem",
		S3ForcePathStyle:              true,
		AWSRequestChecksumCalculation: "when_required",
	}, SudoOptOuts{})

	wantKeys := []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_ENDPOINT_URL",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_SESSION_TOKEN",
		"AWS_CA_BUNDLE", "AWS_S3_FORCE_PATH_STYLE", "S3_FORCE_PATH_STYLE",
		"AWS_REQUEST_CHECKSUM_CALCULATION",
	}
	for _, k := range wantKeys {
		if !envContains(cmd.Env, k) {
			t.Errorf("ToolRclone missing %s: %v", k, cmd.Env)
		}
	}
	// Bool-style entries use "true" not "1" for AWS SDK compatibility.
	if !envEquals(cmd.Env, "AWS_S3_FORCE_PATH_STYLE", "true") {
		t.Errorf("AWS_S3_FORCE_PATH_STYLE should be 'true': %v", cmd.Env)
	}
}

func TestInjectVendor_MCParentEnvFiltered(t *testing.T) {
	cmd := stubCmd()
	// cmd.Env is nil → InjectVendor populates from os.Environ minus the
	// vendor names. With a parent-set MC_HOST_other, it should be filtered.
	t.Setenv("MC_HOST_other", "leak-me")
	InjectVendor(cmd, ToolMC, Resolved{
		MCHostAlias: "local",
		MCHostURL:   "http://u:p@host",
	}, SudoOptOuts{})
	if envContains(cmd.Env, "MC_HOST_other") {
		t.Errorf("parent MC_HOST_other should be filtered for ToolMC: %v", cmd.Env)
	}
	if !envContains(cmd.Env, "MC_HOST_local") {
		t.Errorf("derived MC_HOST_local missing: %v", cmd.Env)
	}
}

func TestSudoOptOutsFromEnv_StorageExtendedFlags(t *testing.T) {
	env := MapEnv(map[string]string{
		"ABC_CLI_NO_DERIVE_AWS_SESSION_TOKEN":  "1",
		"ABC_CLI_NO_DERIVE_S3_FORCE_PATH_STYLE": "true",
		"ABC_CLI_NO_DERIVE_MC_HOST":            "yes",
		"ABC_CLI_NO_DERIVE_MINIO_ROOT_PASSWORD": "on",
	})
	opts := SudoOptOutsFromEnv(env)
	if !opts.AWSSessionToken {
		t.Error("AWSSessionToken opt-out not detected")
	}
	if !opts.S3ForcePathStyle {
		t.Error("S3ForcePathStyle opt-out not detected")
	}
	if !opts.MCHost {
		t.Error("MCHost opt-out not detected")
	}
	if !opts.MinIORootPassword {
		t.Error("MinIORootPassword opt-out not detected")
	}
	if opts.AWSCABundle {
		t.Error("AWSCABundle opt-out spuriously set")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

func envContains(env []string, key string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return true
		}
	}
	return false
}

func envEquals(env []string, key, want string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return kv[len(key)+1:] == want
		}
	}
	return false
}

func stubCmd() *exec.Cmd {
	return &exec.Cmd{}
}
