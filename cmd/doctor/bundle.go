package doctor

// bundle.go implements `abc doctor --bundle` — a single, human-readable,
// credential-safe support file the user can hand the abc team. It assembles
// already-redacted sections (version, platform, redacted config, doctor checks,
// masked env, debug trace) and runs the internal/supportbundle redaction
// guarantee (known-secret exact scrub + value-pattern catch-all) before writing.
//
// Two modes:
//   - passive          `abc doctor --bundle` grabs the most recent debug log.
//   - reproduce+capture `abc doctor --bundle --rerun "data upload x.fastq"`
//                       re-runs the command under --debug=2 and bundles that
//                       exact trace (the gold path for an undiagnosable failure).
//
// See brainstorms/cli-support-bundle/2026-06-02-support-bundle-ux.md.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/supportbundle"
	"github.com/spf13/cobra"
)

const (
	bundleRepoOwner   = "abc-cluster"
	bundleRepoName    = "abc-cluster-cli"
	bundleTraceMaxLen = 256 * 1024 // cap the embedded trace; keep the tail
)

func runBundle(cmd *cobra.Command, ctxName string, skipJob bool, jobTimeout time.Duration) error {
	rerun, _ := cmd.Flags().GetString("rerun")
	outPath, _ := cmd.Flags().GetString("out")
	errw := os.Stderr
	out := os.Stdout

	fmt.Fprintf(errw, "\nabc doctor --bundle — assembling a redacted support file\n")

	// ── Gather doctor results (reuse the same check functions) ────────────────
	g1, cfg, activeCtx := checkConfig(ctxName)
	groups := []groupResult{g1}
	if cfg != nil {
		groups = append(groups, checkConnectivity(cmd.Context(), cfg, activeCtx))
		if !skipJob {
			groups = append(groups, checkWorkload(cmd.Context(), cfg, activeCtx, jobTimeout, errw))
		}
	}

	// ── Resolve the debug trace to embed ──────────────────────────────────────
	var traceLog string
	if strings.TrimSpace(rerun) != "" {
		fmt.Fprintf(errw, "  Re-running under debug capture:  abc %s --debug=2\n", rerun)
		lp, rerr := rerunUnderDebug(cmd.Context(), rerun, errw)
		if rerr != nil {
			fmt.Fprintf(errw, "  (the re-run exited with: %v — its trace is still captured below)\n", rerr)
		}
		traceLog = lp
	}
	if traceLog == "" {
		if lp, err := debuglog.LatestLogPath(); err == nil {
			traceLog = lp
		}
	}

	whoami := bundleWhoami(activeCtx)
	now := time.Now()

	sections := []supportbundle.Section{
		{Title: "1. version", Body: renderVersionSection(cmd.Context())},
		{Title: "2. platform", Body: renderPlatformSection()},
		{Title: "3. config (redacted)", Body: renderConfigSection(cfg)},
		{Title: "4. doctor checks", Body: renderChecksSection(groups)},
		{Title: "5. environment (ABC_*, redacted)", Body: renderEnvSection()},
		{Title: "6. debug trace", Body: renderTraceSection(traceLog)},
	}

	secrets := collectSecrets(activeCtx)
	text := supportbundle.Assemble(supportbundle.Input{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Whoami:      whoami,
		Sections:    sections,
		Secrets:     secrets,
	})

	// Runtime belt-and-braces: never write a file where a known secret survived.
	if n := countLeaks(text, secrets); n > 0 {
		return fmt.Errorf("internal error: %d known-secret occurrence(s) survived redaction — refusing to write the bundle", n)
	}

	if strings.TrimSpace(outPath) == "" {
		outPath = fmt.Sprintf("abc-support-%s-%s.txt", sanitizeForFilename(whoami), now.Format("20060102-150405"))
	}
	if err := os.WriteFile(outPath, []byte(text), 0o600); err != nil {
		return fmt.Errorf("write support bundle: %w", err)
	}

	fmt.Fprintf(out, "\n✓ Wrote support bundle: %s  (%s)\n", outPath, humanSize(len(text)))
	fmt.Fprintln(out, "  It is redacted — no tokens or secret keys. Open it to verify, then send it to the abc team.")
	fmt.Fprintln(out, "  Contains: version, platform, config (redacted), doctor checks, environment, debug trace.")
	return nil
}

// rerunUnderDebug re-executes this binary with the given argument string plus
// --debug=2 (equals form, to sidestep the optional-int footgun), then returns
// the path of the freshly written debug log. The child's output is forwarded to
// w so the user sees their command run; the log file carries the structured,
// redacted trace we actually bundle.
func rerunUnderDebug(ctx context.Context, rerun string, w io.Writer) (string, error) {
	args, err := splitArgs(rerun)
	if err != nil {
		return "", err
	}
	if len(args) == 0 {
		return "", fmt.Errorf("--rerun: empty command")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate self for --rerun: %w", err)
	}
	args = append(args, "--debug=2")
	c := exec.CommandContext(ctx, exe, args...)
	c.Env = os.Environ()
	c.Stdout = w
	c.Stderr = w
	runErr := c.Run()
	lp, _ := debuglog.LatestLogPath()
	return lp, runErr
}

// ── Section renderers (each is source-level redacted; Layers 2+3 run later) ───

func renderVersionSection(ctx context.Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  abc        %s\n", state.CLIVersion)

	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rel, err := utils.FetchLatestReleaseWithContext(tctx, bundleRepoOwner, bundleRepoName)
	if err != nil {
		fmt.Fprintf(&b, "  latest     (check skipped: %v)\n", err)
	} else {
		fmt.Fprintf(&b, "  latest     %s\n", strings.TrimSpace(rel.TagName))
	}
	return b.String()
}

func renderPlatformSection() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  os/arch    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "  go         %s\n", runtime.Version())
	fmt.Fprintf(&b, "  shell      %s    term  %s    lang  %s\n",
		envOr("SHELL", "?"), envOr("TERM", "?"), envOr("LANG", envOr("LC_ALL", "?")))
	if k := unameDescription(); k != "" {
		fmt.Fprintf(&b, "  kernel     %s\n", k)
	}
	return b.String()
}

func renderConfigSection(cfg *config.Config) string {
	if cfg == nil {
		return "  (config.yaml did not load — see doctor checks below)"
	}
	var b strings.Builder
	for _, kv := range cfg.AllKeys() {
		fmt.Fprintf(&b, "  %-52s %s\n", kv[0], kv[1])
	}
	return b.String()
}

func renderChecksSection(groups []groupResult) string {
	var b strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&b, "  [%s]\n", g.Name)
		for _, c := range g.Checks {
			detail := ""
			if c.Detail != "" {
				detail = "  " + c.Detail
			}
			fmt.Fprintf(&b, "    %s  %-34s%s\n", statusGlyph(c.Status), c.Name, detail)
		}
	}
	return b.String()
}

func renderEnvSection() string {
	var keys []string
	for _, e := range os.Environ() {
		i := strings.IndexByte(e, '=')
		if i < 0 {
			continue
		}
		k := e[:i]
		if strings.HasPrefix(k, "ABC_") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "  (no ABC_* environment variables set)"
	}
	var b strings.Builder
	for _, k := range keys {
		v := os.Getenv(k)
		if isSensitiveEnvKey(k) {
			v = maskValue(v)
		}
		fmt.Fprintf(&b, "  %s=%s\n", k, v)
	}
	return b.String()
}

func renderTraceSection(path string) string {
	if strings.TrimSpace(path) == "" {
		return "  (no debug log found)\n" +
			"  Reproduce first with:  ABC_CLI_DEBUG=2 abc <your failing command>\n" +
			"  then re-run this bundle — or use:  abc doctor --bundle --rerun \"<your failing command>\""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("  source: %s\n  (could not read trace: %v)", path, err)
	}
	truncated := false
	if len(data) > bundleTraceMaxLen {
		data = data[len(data)-bundleTraceMaxLen:]
		truncated = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  source: %s\n", path)
	b.WriteString("  (structured JSON debug log — already field/value redacted at write time)\n")
	if truncated {
		fmt.Fprintf(&b, "  (showing the last %s; earlier lines omitted)\n", humanSize(bundleTraceMaxLen))
	}
	b.WriteString("\n")
	b.Write(data)
	return b.String()
}

// ── Secret collection (Layer-2 source) ────────────────────────────────────────

func collectSecrets(activeCtx *config.Context) []string {
	secrets := activeCtx.SecretValues()
	// Also scrub the raw values of sensitive ABC_*/VAULT env vars, in case any
	// section (or the trace) echoes them.
	for _, k := range []string{
		"ABC_API_TOKEN", "ABC_NODE_PASSWORD", "ABC_TAILSCALE_AUTH_KEY",
		"ABC_UPLOAD_TOKEN", "VAULT_TOKEN", "NOMAD_TOKEN",
	} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			secrets = append(secrets, v)
		}
	}
	return secrets
}

func countLeaks(text string, secrets []string) int {
	n := 0
	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if len(s) < 6 {
			continue
		}
		n += strings.Count(text, s)
	}
	return n
}

// ── small helpers ─────────────────────────────────────────────────────────────

func bundleWhoami(activeCtx *config.Context) string {
	if activeCtx != nil {
		if activeCtx.Auth != nil && strings.TrimSpace(activeCtx.Auth.Whoami) != "" {
			return strings.TrimSpace(activeCtx.Auth.Whoami)
		}
		if v := strings.TrimSpace(activeCtx.Admin.Whoami); v != "" {
			return v
		}
	}
	if u := strings.TrimSpace(os.Getenv("USER")); u != "" {
		return u
	}
	return "user"
}

func isSensitiveEnvKey(k string) bool {
	up := strings.ToUpper(k)
	for _, frag := range []string{"TOKEN", "PASSWORD", "SECRET", "KEY", "PASS"} {
		if strings.Contains(up, frag) {
			return true
		}
	}
	return false
}

func maskValue(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return strings.Repeat("•", len(v))
	}
	return v[:4] + strings.Repeat("•", 8)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func unameDescription() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	c := exec.Command("uname", "-sr")
	outb, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(outb))
}

func sanitizeForFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "user"
	}
	return out
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// splitArgs splits a command string into argv, honouring single and double
// quotes (so filenames with spaces survive). It is intentionally minimal — no
// escape sequences, no variable expansion — which is sufficient for the
// `--rerun "data upload my file.fastq"` use case.
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inSingle, inDouble, has := false, false, false
	for _, r := range s {
		switch {
		case inSingle:
			if r == '\'' {
				inSingle = false
			} else {
				cur.WriteRune(r)
			}
		case inDouble:
			if r == '"' {
				inDouble = false
			} else {
				cur.WriteRune(r)
			}
		case r == '\'':
			inSingle = true
			has = true
		case r == '"':
			inDouble = true
			has = true
		case r == ' ' || r == '\t':
			if has {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("--rerun: unbalanced quote in %q", s)
	}
	if has {
		args = append(args, cur.String())
	}
	return args, nil
}
