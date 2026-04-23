package cmd

import (
	"context"
	"strings"
	"testing"
)

func TestMaybePrintCLIUpdateNotice_PrintsWhenNewerReleaseExists(t *testing.T) {
	orig := fetchLatestCLITag
	t.Cleanup(func() { fetchLatestCLITag = orig })
	fetchLatestCLITag = func(context.Context) (string, error) {
		return "v1.5.0", nil
	}

	t.Setenv(updateCheckDisableEnv, "")
	var b strings.Builder
	maybePrintCLIUpdateNotice(&b, "v1.4.0", false)

	out := b.String()
	if !strings.Contains(out, "update available: v1.5.0 (current v1.4.0)") {
		t.Fatalf("expected update notice, got: %q", out)
	}
	if !strings.Contains(out, "--version v1.5.0") {
		t.Fatalf("expected install script command with version, got: %q", out)
	}
}

func TestMaybePrintCLIUpdateNotice_DoesNotPrintWhenDisabled(t *testing.T) {
	orig := fetchLatestCLITag
	t.Cleanup(func() { fetchLatestCLITag = orig })
	fetchLatestCLITag = func(context.Context) (string, error) {
		return "v9.9.9", nil
	}

	t.Setenv(updateCheckDisableEnv, "1")
	var b strings.Builder
	maybePrintCLIUpdateNotice(&b, "v1.0.0", false)
	if b.Len() != 0 {
		t.Fatalf("expected no output when disabled, got: %q", b.String())
	}
}

func TestMaybePrintCLIUpdateNotice_DoesNotPrintForDevVersion(t *testing.T) {
	orig := fetchLatestCLITag
	t.Cleanup(func() { fetchLatestCLITag = orig })
	fetchLatestCLITag = func(context.Context) (string, error) {
		return "v9.9.9", nil
	}

	var b strings.Builder
	maybePrintCLIUpdateNotice(&b, "dev", false)
	if b.Len() != 0 {
		t.Fatalf("expected no output for dev version, got: %q", b.String())
	}
}

func TestMaybePrintCLIUpdateNotice_DoesNotPrintWhenAlreadyLatest(t *testing.T) {
	orig := fetchLatestCLITag
	t.Cleanup(func() { fetchLatestCLITag = orig })
	fetchLatestCLITag = func(context.Context) (string, error) {
		return "v1.5.0", nil
	}

	var b strings.Builder
	maybePrintCLIUpdateNotice(&b, "v1.5.0", false)
	if b.Len() != 0 {
		t.Fatalf("expected no output when already latest, got: %q", b.String())
	}
}

func TestNormalizeSemverTag(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{name: "already prefixed", input: "v1.2.3", want: "v1.2.3", ok: true},
		{name: "without prefix", input: "1.2.3", want: "v1.2.3", ok: true},
		{name: "with whitespace", input: " 1.2.3 ", want: "v1.2.3", ok: true},
		{name: "invalid", input: "foo", want: "", ok: false},
		{name: "dev", input: "dev", want: "", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeSemverTag(tc.input)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("normalizeSemverTag(%q) = (%q, %v), want (%q, %v)", tc.input, got, ok, tc.want, tc.ok)
			}
		})
	}
}
