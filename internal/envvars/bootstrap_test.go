package envvars

import (
	"testing"
)

func setEnv(t *testing.T, k, v string) {
	t.Helper()
	t.Setenv(k, v)
}

func TestLookupEnv_CanonicalSet(t *testing.T) {
	setEnv(t, "ABC_API_ADDR", "https://api.example")
	v, src, ok := LookupEnv("ABC_API_ADDR")
	if !ok || v != "https://api.example" || src != SourceABCEnv {
		t.Errorf("got (%q, %v, %v); want (https://api.example, abc-env, true)", v, src, ok)
	}
}

func TestLookupEnv_Unset(t *testing.T) {
	// ABC_API_AS_USER is unlikely to be set in the test environment.
	v, src, ok := LookupEnv("ABC_API_AS_USER")
	if ok {
		t.Skip("ABC_API_AS_USER unexpectedly set in test environment; skipping")
	}
	if v != "" || src != SourceUnset {
		t.Errorf("got (%q, %v, %v); want (\"\", unset, false)", v, src, ok)
	}
}

func TestIsTruthy(t *testing.T) {
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
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown registry name")
		}
	}()
	_, _, _ = LookupEnv("ABC_TOTALLY_FAKE_NAME")
}
