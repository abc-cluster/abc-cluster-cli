package shellcheck

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Finding is a single lint result. Line is 1-based; 0 means "whole-script".
type Finding struct {
	Line int
	Code string // abc-specific code (e.g. ABC001) or upstream SC code
	Msg  string
}

func (f Finding) String() string {
	if f.Line == 0 {
		return fmt.Sprintf("%s: %s", f.Code, f.Msg)
	}
	return fmt.Sprintf("line %d: %s: %s", f.Line, f.Code, f.Msg)
}

// LintNomadHeredoc validates a bash script intended to be embedded inside a
// Nomad HCL2 heredoc (e.g. `args = ["-c", <<-EOH ... EOH]`).
//
// Runs three passes:
//  1. abc-specific pre-checks (ABC001–004) — Nomad escape rules, UID collisions.
//  2. mvdan.cc/sh parse — embedded Go bash parser; catches structural errors
//     even when shellcheck is unavailable.
//  3. system `shellcheck` — full SC* rule set; skipped (not failed) when the
//     binary is not on PATH.
//
// Returns a flat list of findings (empty on clean) plus any invocation error.
// A missing system shellcheck is NOT an invocation error — the parse pass
// already provides a meaningful baseline.
func LintNomadHeredoc(ctx context.Context, script string, opts Options) ([]Finding, error) {
	findings := preCheckNomad(script)

	if perr := Parse(script); perr != nil {
		findings = append(findings, Finding{Code: "PARSE", Msg: perr.Error()})
	}

	out, err := Lint(ctx, script, opts)
	switch {
	case errors.Is(err, ErrShellcheckUnavailable):
		// Soft skip — pre-checks + parse already ran.
	case err != nil && out != "":
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			findings = append(findings, parseGcc(line))
		}
	case err != nil:
		return findings, err
	}
	return findings, nil
}

var (
	reUnescapedBraceVar = regexp.MustCompile(`(^|[^$])\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	reDoubleDollarVar   = regexp.MustCompile(`\$\$[A-Za-z_][A-Za-z0-9_]*`)
	reSlashSlash        = regexp.MustCompile(`^\s*//`)
	reUseraddUID1000    = regexp.MustCompile(`useradd\b[^\n]*\s-u\s*1000\b`)
	reGcc               = regexp.MustCompile(`^[^:]*:(\d+):\d+:\s*\w+:\s*(.*?)(?:\s+\[(SC\d+)\])?$`)
)

// preCheckNomad runs the abc-specific lint passes that shellcheck doesn't know
// about. These catch the four bug classes from the positron MVP debugging
// session (see mvp/positron/SHELLCHECK-FOLLOWUP.md).
func preCheckNomad(script string) []Finding {
	var findings []Finding
	for i, line := range strings.Split(script, "\n") {
		ln := i + 1

		// ABC001: ${VAR} inside a Nomad heredoc is interpreted by the HCL2
		// parser as a template interpolation. Whitelist NOMAD_* names which
		// Nomad provides at template render time. Everything else must use
		// $${VAR} (HCL escape) so bash sees ${VAR}.
		for _, m := range reUnescapedBraceVar.FindAllStringSubmatchIndex(line, -1) {
			name := line[m[4]:m[5]]
			if strings.HasPrefix(name, "NOMAD_") {
				continue
			}
			findings = append(findings, Finding{
				Line: ln,
				Code: "ABC001",
				Msg:  fmt.Sprintf("unescaped ${%s} in Nomad heredoc — write $${%s} so bash sees ${%s} (or drop braces: $%s)", name, name, name, name),
			})
		}

		// ABC002: $$VAR is a common mis-escape. In HCL2 heredocs only ${...}
		// and %{...} are template syntax; bare $VAR passes through. Writing
		// $$VAR sends literal $$VAR to bash, which expands $$ → PID, leaving
		// "<PID>VAR" garbage.
		for _, m := range reDoubleDollarVar.FindAllStringIndex(line, -1) {
			tok := line[m[0]:m[1]]
			findings = append(findings, Finding{
				Line: ln,
				Code: "ABC002",
				Msg:  fmt.Sprintf("`%s` becomes `<PID>%s` in bash — HCL2 heredocs don't need to escape bare `$VAR`, just write `$%s`", tok, tok[2:], tok[2:]),
			})
		}

		// ABC003: `//` is not a bash comment. Common typo from C/Go authors;
		// bash will try to run it as a path.
		if reSlashSlash.MatchString(line) {
			findings = append(findings, Finding{
				Line: ln,
				Code: "ABC003",
				Msg:  "bash comments start with `#`, not `//`",
			})
		}
	}

	// ABC004: `useradd -u 1000` collides with the default `ubuntu` user that
	// ships in ubuntu:24.04 base images. Either userdel ubuntu first or omit -u.
	if reUseraddUID1000.MatchString(script) && !strings.Contains(script, "userdel") {
		findings = append(findings, Finding{
			Code: "ABC004",
			Msg:  "useradd -u 1000 conflicts with the `ubuntu` user in ubuntu:* base images; `userdel -r ubuntu` first or omit -u",
		})
	}

	return findings
}

// parseGcc extracts line/code/msg from a shellcheck --format=gcc line, e.g.
//
//	-:5:3: warning: Use double quotes... [SC2086]
func parseGcc(line string) Finding {
	if m := reGcc.FindStringSubmatch(line); m != nil {
		ln := 0
		fmt.Sscanf(m[1], "%d", &ln)
		code := m[3]
		if code == "" {
			code = "shellcheck"
		}
		return Finding{Line: ln, Code: code, Msg: m[2]}
	}
	return Finding{Code: "shellcheck", Msg: line}
}
