package shellcheck

import (
	"context"
	"strings"
	"testing"
)

func requireShellcheck(t *testing.T) {
	t.Helper()
	if !HasShellcheck() {
		t.Skip("shellcheck binary not on PATH; skipping system-shellcheck test")
	}
}

func TestPreCheck_RejectsUnescapedDollarBrace(t *testing.T) {
	script := `#!/bin/bash
echo "${ABC_WORKBENCH_S3_PREFIX}"
`
	got := preCheckNomad(script)
	if len(got) != 1 || got[0].Code != "ABC001" {
		t.Fatalf("expected one ABC001 finding, got %+v", got)
	}
}

func TestPreCheck_AllowsNomadBracedVar(t *testing.T) {
	script := `#!/bin/bash
echo "${NOMAD_TASK_DIR}/wave"
`
	if got := preCheckNomad(script); len(got) != 0 {
		t.Fatalf("NOMAD_* should be allowed, got %+v", got)
	}
}

func TestPreCheck_AllowsBareDollarVar(t *testing.T) {
	script := `#!/bin/bash
echo "$ABC_WORKBENCH_S3_PREFIX"
`
	if got := preCheckNomad(script); len(got) != 0 {
		t.Fatalf("bare $VAR should be allowed, got %+v", got)
	}
}

func TestPreCheck_RejectsDoubleDollar(t *testing.T) {
	script := `#!/bin/bash
echo "$$ABC_WORKBENCH_S3_PREFIX"
`
	got := preCheckNomad(script)
	if len(got) != 1 || got[0].Code != "ABC002" {
		t.Fatalf("expected one ABC002 finding, got %+v", got)
	}
}

func TestPreCheck_RejectsSlashSlashComment(t *testing.T) {
	script := `#!/bin/bash
// this is wrong
echo ok
`
	got := preCheckNomad(script)
	if len(got) != 1 || got[0].Code != "ABC003" {
		t.Fatalf("expected one ABC003 finding, got %+v", got)
	}
}

func TestPreCheck_RejectsUid1000WithoutUserdel(t *testing.T) {
	script := `#!/bin/bash
useradd -m -s /bin/bash -u 1000 abhi
`
	got := preCheckNomad(script)
	if len(got) != 1 || got[0].Code != "ABC004" {
		t.Fatalf("expected one ABC004 finding, got %+v", got)
	}
}

func TestPreCheck_AcceptsUid1000WithUserdel(t *testing.T) {
	script := `#!/bin/bash
userdel -r ubuntu 2>/dev/null || true
useradd -m -s /bin/bash -u 1000 abhi
`
	if got := preCheckNomad(script); len(got) != 0 {
		t.Fatalf("userdel-then-useradd should pass, got %+v", got)
	}
}

func TestPreCheck_CleanScriptHasNoFindings(t *testing.T) {
	script := `#!/bin/bash
set -euo pipefail
export FOO="bar"
echo "value: $FOO"
echo "task dir: ${NOMAD_TASK_DIR}"
`
	if got := preCheckNomad(script); len(got) != 0 {
		t.Fatalf("clean script should have no findings, got %+v", got)
	}
}

// TestPreCheck_PositronMVPRegression encodes the four bug classes that hit
// during the positron MVP debugging cycle. If any of these patterns slip
// into a future hand-written .nomad.hcl.tmpl, this test catches them.
func TestPreCheck_PositronMVPRegression(t *testing.T) {
	cases := []struct {
		name string
		bash string
		want string
	}{
		{"Nomad eats ${ABC_WORKBENCH_S3_PREFIX}", `if s5cmd ls "${ABC_WORKBENCH_S3_PREFIX}"; then echo found; fi`, "ABC001"},
		{"$$VAR becomes <PID>VAR", `echo "$$ABC_WORKBENCH_S3_PREFIX"`, "ABC002"},
		{"// is not a bash comment", "// hardcoded UID 1000", "ABC003"},
		{"useradd -u 1000 collides with ubuntu user", `useradd -m -s /bin/bash -u 1000 alice`, "ABC004"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings := preCheckNomad(c.bash)
			found := false
			for _, f := range findings {
				if f.Code == c.want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected %s finding, got %+v", c.want, findings)
			}
		})
	}
}

// --- Parse (mvdan.cc/sh) — embedded, always available -----------------------

func TestParse_AcceptsValidBash(t *testing.T) {
	script := `#!/bin/bash
set -euo pipefail
for i in 1 2 3; do
  echo "iter $i"
done
`
	if err := Parse(script); err != nil {
		t.Fatalf("expected clean parse, got %v", err)
	}
}

func TestParse_RejectsUnterminatedHeredoc(t *testing.T) {
	script := `#!/bin/bash
cat <<EOF
hello
`
	if err := Parse(script); err == nil {
		t.Fatal("expected unterminated-heredoc parse error")
	}
}

func TestParse_RejectsMissingFi(t *testing.T) {
	script := `#!/bin/bash
if [ -z "$FOO" ]; then
  echo missing fi
`
	if err := Parse(script); err == nil {
		t.Fatal("expected missing-fi parse error")
	}
}

func TestParse_RejectsUnbalancedQuote(t *testing.T) {
	script := `#!/bin/bash
echo "unterminated
`
	if err := Parse(script); err == nil {
		t.Fatal("expected unterminated-quote parse error")
	}
}

// --- Lint (system shellcheck — soft skip if unavailable) --------------------

func TestLint_ReturnsErrShellcheckUnavailableWhenMissing(t *testing.T) {
	if HasShellcheck() {
		t.Skip("shellcheck IS available; can't test the unavailable path here")
	}
	_, err := Lint(context.Background(), "echo ok\n", Default())
	if err == nil || !strings.Contains(err.Error(), "shellcheck binary not found") {
		t.Fatalf("expected ErrShellcheckUnavailable, got %v", err)
	}
}

func TestLint_FlagsSC2086(t *testing.T) {
	requireShellcheck(t)
	script := `#!/bin/bash
FOO=$1
ls $FOO
`
	opts := Default()
	opts.Severity = "info"
	out, err := Lint(context.Background(), script, opts)
	if err == nil {
		t.Fatalf("expected shellcheck to flag SC2086, got clean run; out=%q", out)
	}
	if !strings.Contains(out, "SC2086") {
		t.Fatalf("expected SC2086 in output, got %q", out)
	}
}

func TestLint_AcceptsCleanScript(t *testing.T) {
	requireShellcheck(t)
	script := `#!/bin/bash
set -euo pipefail
foo="$1"
echo "got: $foo"
`
	if out, err := Lint(context.Background(), script, Default()); err != nil {
		t.Fatalf("clean script should pass shellcheck; got err=%v out=%q", err, out)
	}
}

func TestLintNomadHeredoc_CombinesPasses(t *testing.T) {
	script := `#!/bin/bash
echo "${ABC_WORKBENCH_S3_PREFIX}"
ls $UNQUOTED
`
	opts := Default()
	opts.Severity = "info"
	findings, err := LintNomadHeredoc(context.Background(), script, opts)
	if err != nil {
		t.Logf("invocation note: %v", err)
	}
	var sawABC001 bool
	for _, f := range findings {
		if f.Code == "ABC001" {
			sawABC001 = true
		}
	}
	if !sawABC001 {
		t.Errorf("expected ABC001 finding for ${ABC_WORKBENCH_S3_PREFIX}; got %+v", findings)
	}
	// SC* findings only when shellcheck is on PATH; if it is, the unquoted
	// $UNQUOTED expansion must have been flagged.
	if HasShellcheck() {
		var sawSC bool
		for _, f := range findings {
			if strings.HasPrefix(f.Code, "SC") {
				sawSC = true
				break
			}
		}
		if !sawSC {
			t.Errorf("shellcheck on PATH but no SC* finding; got %+v", findings)
		}
	}
}
