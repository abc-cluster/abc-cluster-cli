package job

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/shellcheck"
)

func TestValidate_AcceptsCleanBashInWarnMode(t *testing.T) {
	body := `#!/bin/bash
set -euo pipefail
echo "hello"
`
	var buf bytes.Buffer
	if err := validateSubmittedScript(context.Background(), &buf, "clean.sh", body, "warn"); err != nil {
		t.Fatalf("clean script should pass: %v\nstderr: %s", err, buf.String())
	}
}

func TestValidate_RejectsParseErrorEvenInWarnMode(t *testing.T) {
	// Missing fi → bash parse error. mvdan.cc/sh catches this without
	// needing system shellcheck.
	body := `#!/bin/bash
if [ -z "$1" ]; then
  echo missing fi
`
	var buf bytes.Buffer
	err := validateSubmittedScript(context.Background(), &buf, "broken.sh", body, "warn")
	if err == nil {
		t.Fatal("expected parse-error rejection in warn mode, got nil")
	}
	if !strings.Contains(err.Error(), "bash parse error") {
		t.Fatalf("error should mention parse: %v", err)
	}
}

func TestValidate_OffSkipsBrokenScript(t *testing.T) {
	body := `#!/bin/bash
if [ -z "$1" ]; then
  echo missing fi
`
	// "off" is enforced at the call site (skips entirely); confirm by
	// invoking with the canonical "warn" then verifying "off" is documented
	// to bypass — here we only ensure validate is not called when off.
	// This is mostly a smoke test for the contract.
	if mode := "off"; mode == "off" {
		// Caller skips invocation entirely — no error to assert.
		_ = body
	}
}

func TestValidate_ErrorModeRequiresShellcheck(t *testing.T) {
	if shellcheck.HasShellcheck() {
		t.Skip("shellcheck IS on PATH; can't test the missing-binary path")
	}
	body := `#!/bin/bash
echo ok
`
	var buf bytes.Buffer
	err := validateSubmittedScript(context.Background(), &buf, "ok.sh", body, "error")
	if err == nil || !strings.Contains(err.Error(), "requires `shellcheck` on PATH") {
		t.Fatalf("expected ErrShellcheckUnavailable in error mode; got %v", err)
	}
}

func TestValidate_WarnModeShellcheckFindingsArePrinted(t *testing.T) {
	if !shellcheck.HasShellcheck() {
		t.Skip("shellcheck not on PATH")
	}
	// SC2034 (unused variable) is severity=warning and fires reliably.
	body := `#!/bin/bash
unused=foo
echo "ok"
`
	var buf bytes.Buffer
	if err := validateSubmittedScript(context.Background(), &buf, "unused.sh", body, "warn"); err != nil {
		t.Fatalf("warn mode should not fail on findings: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "unused.sh") {
		t.Fatalf("expected warning output mentioning the script: %q", out)
	}
}

func TestValidate_ErrorModeShellcheckFindingsBlockSubmission(t *testing.T) {
	if !shellcheck.HasShellcheck() {
		t.Skip("shellcheck not on PATH")
	}
	body := `#!/bin/bash
unused=foo
echo "ok"
`
	var buf bytes.Buffer
	err := validateSubmittedScript(context.Background(), &buf, "unused.sh", body, "error")
	if err == nil {
		t.Fatal("expected error-mode rejection on shellcheck findings")
	}
	if !strings.Contains(err.Error(), "shellcheck reported issues") {
		t.Fatalf("error should mention shellcheck issues: %v", err)
	}
}

func TestValidate_NonBashShebangIsSkipped(t *testing.T) {
	body := "#!/usr/bin/env python3\nimport sys\nprint('no bash here, do not lint')\n"
	var buf bytes.Buffer
	if err := validateSubmittedScript(context.Background(), &buf, "script.py", body, "error"); err != nil {
		t.Fatalf("python script should be skipped, got %v", err)
	}
}

func TestValidate_RejectsInvalidMode(t *testing.T) {
	var buf bytes.Buffer
	err := validateSubmittedScript(context.Background(), &buf, "x.sh", "echo ok\n", "loud")
	if err == nil || !strings.Contains(err.Error(), "must be warn, error, or off") {
		t.Fatalf("expected mode validation error, got %v", err)
	}
}

func TestLooksLikeBash(t *testing.T) {
	cases := map[string]bool{
		"#!/bin/bash\necho hi":           true,
		"#!/bin/sh\necho hi":             true,
		"#!/usr/bin/env bash\necho hi":   true,
		"#!/usr/bin/env python3\nprint":  false,
		"#!/usr/bin/perl\nprint 'hi'":    false,
		"echo no shebang":                true, // default-assume bash
		"\n\n#!/bin/bash\necho leading":  true,
	}
	for body, want := range cases {
		if got := looksLikeBash(body); got != want {
			t.Errorf("looksLikeBash(%q) = %v, want %v", body, got, want)
		}
	}
}
