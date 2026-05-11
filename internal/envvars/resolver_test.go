package envvars

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// flagMap is a tiny FlagLookup helper for tests.
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

// newTestResolver builds a Resolver with all three lookups backed by
// in-memory maps and a captured warning sink.
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

// ── Precedence: flag > ABC env > alias env > vendor env > context > default ──

func TestResolve_FlagBeatsEverything(t *testing.T) {
	r, _ := newTestResolver(
		map[string]string{"address": "from-flag"},
		map[string]string{"ABC_API_ADDR": "from-env", "NOMAD_ADDR": "from-vendor"},
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
		map[string]string{"ABC_API_ADDR": "from-abc", "NOMAD_ADDR": "from-nomad"},
		map[string]string{"url": "from-context"},
		true,
	)
	v, src, err := r.Resolve("ABC_API_ADDR")
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

func TestResolve_AliasResolvesAndWarns(t *testing.T) {
	r, warn := newTestResolver(
		nil,
		map[string]string{"ABC_API_ENDPOINT": "from-alias"},
		nil,
		true,
	)
	v, src, err := r.Resolve("ABC_API_ADDR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "from-alias" || src != SourceABCEnvAlias {
		t.Errorf("got (%q, %v); want (from-alias, abc-env-alias)", v, src)
	}
	if !strings.Contains(warn.String(), "ABC_API_ENDPOINT is deprecated") {
		t.Errorf("expected deprecation warning, got: %q", warn.String())
	}
	if !strings.Contains(warn.String(), "use ABC_API_ADDR") {
		t.Errorf("expected migration hint, got: %q", warn.String())
	}
}

func TestResolve_AliasWarningEmittedOncePerAlias(t *testing.T) {
	r, warn := newTestResolver(
		nil,
		map[string]string{"ABC_API_ENDPOINT": "x"},
		nil,
		true,
	)
	_, _, _ = r.Resolve("ABC_API_ADDR")
	_, _, _ = r.Resolve("ABC_API_ADDR")
	_, _, _ = r.Resolve("ABC_API_ADDR")
	count := strings.Count(warn.String(), "is deprecated")
	if count != 1 {
		t.Errorf("expected 1 deprecation warning across 3 resolutions, got %d", count)
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
		false, // no ABC context
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
		true, // ABC context configured: don't nag
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
	// ABC_API_ADDR has no Default; with nothing set, expect unset.
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

func TestRegistry_LookupCanonicalAndAlias(t *testing.T) {
	// Canonical
	e, ok := Lookup("ABC_API_ADDR")
	if !ok || e.Name != "ABC_API_ADDR" {
		t.Errorf("Lookup(ABC_API_ADDR) = (%v, %v); want canonical entry", e, ok)
	}
	// Alias
	e2, ok := Lookup("ABC_API_ENDPOINT")
	if !ok || e2.Name != "ABC_API_ADDR" {
		t.Errorf("Lookup(ABC_API_ENDPOINT) should resolve to ABC_API_ADDR; got (%v, %v)", e2, ok)
	}
}

func TestRegistry_NoForbiddenPatterns(t *testing.T) {
	// Reject-list per spec §B.4. Note: alias entries from older names
	// (ABC_DISABLE_*, ABC_*_OFF) are not in our Aliases — we never had any.
	// Tier-coupled names must never appear.
	for _, e := range Registry {
		names := append([]string{e.Name}, e.Aliases...)
		for _, n := range names {
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
}

func TestRegistry_AliasMustNotCollideWithAnyCanonical(t *testing.T) {
	canonicalSet := map[string]bool{}
	for _, e := range Registry {
		canonicalSet[e.Name] = true
	}
	for _, e := range Registry {
		for _, a := range e.Aliases {
			if canonicalSet[a] {
				t.Errorf("alias %q on entry %q collides with another canonical name", a, e.Name)
			}
		}
	}
}

func TestRegistry_AliasUniqueAcrossEntries(t *testing.T) {
	seen := map[string]string{} // alias -> canonical
	for _, e := range Registry {
		for _, a := range e.Aliases {
			if prev, dup := seen[a]; dup {
				t.Errorf("alias %q claimed by both %q and %q", a, prev, e.Name)
			}
			seen[a] = e.Name
		}
	}
}

// ── Subprocess injection ────────────────────────────────────────────────

func TestInjectVendor_NomadSetsAllFour(t *testing.T) {
	// Need exec.Cmd; build a stub.
	cmd := stubCmd()
	cmd.Env = []string{"PATH=/usr/bin"} // pre-populated, non-empty
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
	// Simulate parent shell having NOMAD_ADDR set; opt out the derivation.
	cmd.Env = []string{"PATH=/usr/bin", "NOMAD_ADDR=parent-value"}
	InjectVendor(cmd, ToolNomad, Resolved{
		NomadAddr: "abc-derived-value",
	}, SudoOptOuts{NomadAddr: true})

	// With opt-out, the parent value is kept and the derived one is NOT added.
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

// stubCmd builds a minimal *exec.Cmd for tests. Tests inspect cmd.Env
// after InjectVendor; nothing is actually executed.
func stubCmd() *exec.Cmd {
	return &exec.Cmd{}
}
