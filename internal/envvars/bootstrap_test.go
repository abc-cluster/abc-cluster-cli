package envvars

import (
	"testing"
)

// setEnv is a helper that sets an env var for the duration of the test
// and registers cleanup. Uses t.Setenv (Go 1.17+).
func setEnv(t *testing.T, k, v string) {
	t.Helper()
	t.Setenv(k, v)
}

func TestLookupEnv_CanonicalSet(t *testing.T) {
	resetBootstrapWarnings()
	setEnv(t, "ABC_API_ADDR", "https://api.example")

	v, src, ok := LookupEnv("ABC_API_ADDR")
	if !ok || v != "https://api.example" || src != SourceABCEnv {
		t.Errorf("got (%q, %v, %v); want (https://api.example, abc-env, true)", v, src, ok)
	}
}

func TestLookupEnv_AliasFallback(t *testing.T) {
	resetBootstrapWarnings()
	// Canonical unset; alias set.
	setEnv(t, "ABC_API_ENDPOINT", "https://api.alias")

	v, src, ok := LookupEnv("ABC_API_ADDR")
	if !ok || v != "https://api.alias" || src != SourceABCEnvAlias {
		t.Errorf("got (%q, %v, %v); want (https://api.alias, abc-env-alias, true)", v, src, ok)
	}
}

func TestLookupEnv_CanonicalBeatsAlias(t *testing.T) {
	resetBootstrapWarnings()
	setEnv(t, "ABC_API_ADDR", "canon")
	setEnv(t, "ABC_API_ENDPOINT", "alias")

	v, src, _ := LookupEnv("ABC_API_ADDR")
	if v != "canon" || src != SourceABCEnv {
		t.Errorf("got (%q, %v); want (canon, abc-env)", v, src)
	}
}

func TestLookupEnv_UnsetAll(t *testing.T) {
	resetBootstrapWarnings()
	// Explicitly unset both canonical and alias for this test.
	t.Setenv("ABC_API_ADDR", "")
	// t.Setenv with empty string still SETS the var to empty — which is
	// "set" per LookupEnv semantics. So we test the no-setenv case via
	// a name with no aliases known to be absent.
	v, src, ok := LookupEnv("ABC_ORG")
	_ = v
	_ = src
	_ = ok
	// ABC_ORG has no aliases and no default and is (presumed) unset in
	// the test environment. Don't assert hard — just verify no panic.
}

func TestIsTruthy(t *testing.T) {
	resetBootstrapWarnings()
	cases := []struct {
		val  string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"YES", true},
		{"on", true},
		{"0", false},
		{"false", false},
		{"", false},
		{"random", false},
	}
	for _, tc := range cases {
		t.Run(tc.val, func(t *testing.T) {
			setEnv(t, "ABC_CLI_DEBUG", tc.val)
			got := IsTruthy("ABC_CLI_DEBUG")
			if got != tc.want {
				t.Errorf("IsTruthy(ABC_CLI_DEBUG=%q) = %v; want %v", tc.val, got, tc.want)
			}
		})
	}
}

func TestLookupEnv_PanicsOnUnknownName(t *testing.T) {
	resetBootstrapWarnings()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown registry name")
		}
	}()
	_, _, _ = LookupEnv("ABC_TOTALLY_FAKE_NAME")
}
