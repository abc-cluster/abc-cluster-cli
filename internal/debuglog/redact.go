package debuglog

import (
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// ─── Field-name redaction ─────────────────────────────────────────────────────

// sensitiveKeys is the set of slog attribute key names (lowercased) whose
// values are always replaced with "[REDACTED]" regardless of content.
var sensitiveKeys = map[string]bool{
	"password":           true,
	"pass":               true,
	"passwd":             true,
	"private_key":        true,
	"key_material":       true,
	"pem_data":           true,
	"pem":                true,
	"access_token":       true,
	"token":              true,
	"bearer_token":       true,
	"auth_key":           true,
	"tailscale_auth_key": true,
	"ts_authkey":         true,
	"ts_key":             true,
	"secret":             true,
	"credential":         true,
	"credentials":        true,
}

func isSensitiveKey(key string) bool {
	return sensitiveKeys[strings.ToLower(key)]
}

// ─── Value-pattern redaction ──────────────────────────────────────────────────

type valuePattern struct {
	re          *regexp.Regexp
	replacement string
}

var valuePatterns = []valuePattern{
	{
		// PEM private key blocks
		re:          regexp.MustCompile(`(?s)-----BEGIN .{0,30}PRIVATE KEY-----.*?-----END .{0,30}PRIVATE KEY-----`),
		replacement: "[REDACTED: private-key]",
	},
	{
		// Tailscale pre-auth keys
		re:          regexp.MustCompile(`tskey-auth-[A-Za-z0-9_-]{10,}`),
		replacement: "[REDACTED: tailscale-key]",
	},
	{
		// HTTP Bearer tokens
		re:          regexp.MustCompile(`[Bb]earer [A-Za-z0-9.\-_]{20,}`),
		replacement: "Bearer [REDACTED]",
	},
	{
		// Passwords embedded in connection strings: scheme://user:pass@host
		re:          regexp.MustCompile(`://[^:@/\s]+:[^@/\s]{6,}@`),
		replacement: "://[REDACTED]@",
	},
}

// redactValue applies value-pattern rules to s and returns the scrubbed result.
func redactValue(s string) string {
	for _, p := range valuePatterns {
		s = p.re.ReplaceAllString(s, p.replacement)
	}
	return s
}

// ─── Attr scrubbing ───────────────────────────────────────────────────────────

// scrubAttr returns a sanitised copy of attr:
//   - Sensitive keys → value replaced with "[REDACTED]"
//   - Groups → recursed
//   - String values → value patterns applied
func scrubAttr(a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, "[REDACTED]")
	}
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		cleaned := make([]slog.Attr, len(attrs))
		for i, sub := range attrs {
			cleaned[i] = scrubAttr(sub)
		}
		return slog.Group(a.Key, attrsToAny(cleaned)...)
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, redactValue(a.Value.String()))
	}
	return a
}

func attrsToAny(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))
	for i, a := range attrs {
		out[i] = a
	}
	return out
}

// ─── Argv scrubbing ───────────────────────────────────────────────────────────

// sensitiveFlags is the set of CLI flag names (without leading dashes) whose
// next positional value (or =value suffix) is replaced with "[REDACTED]".
var sensitiveFlags = map[string]bool{
	"password":           true,
	"pass":               true,
	"access-token":       true,
	"token":              true,
	"tailscale-auth-key": true,
	"private-key":        true,
	"ssh-key":            true,
}

// RedactArgv returns a copy of argv with sensitive flag values replaced.
func RedactArgv(argv []string) []string {
	out := make([]string, len(argv))
	copy(out, argv)
	for i := 0; i < len(out); i++ {
		arg := out[i]
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		stripped := strings.TrimLeft(arg, "-")

		// --flag=value form
		if eqIdx := strings.IndexByte(stripped, '='); eqIdx >= 0 {
			name := stripped[:eqIdx]
			if sensitiveFlags[name] {
				out[i] = "--" + name + "=[REDACTED]"
			}
			continue
		}

		// --flag value form (next element is the value)
		if sensitiveFlags[stripped] && i+1 < len(out) {
			i++
			out[i] = "[REDACTED]"
		}
	}
	return out
}

// ─── Environment variable snapshot ───────────────────────────────────────────

// EnvSnapshot returns a map of relevant environment variables for inclusion in
// the cli.invocation log record. Sensitive variable values are replaced with
// "set" or "not set" — their actual values are never written to the log.
func EnvSnapshot() map[string]string {
	snap := make(map[string]string)

	sensitive := []string{
		"ABC_NODE_PASSWORD",
		"ABC_ACCESS_TOKEN",
		"ABC_TAILSCALE_AUTH_KEY",
	}
	for _, k := range sensitive {
		if os.Getenv(k) != "" {
			snap[k] = "set"
		} else {
			snap[k] = "not set"
		}
	}

	tracked := []string{
		"ABC_DEBUG",
		"ABC_CLUSTER",
		"ABC_CLI_SUDO_MODE",
		"ABC_CLI_CLOUD_MODE",
		"ABC_CLI_EXP_MODE",
		"SSH_AUTH_SOCK",
		"USER",
	}
	for _, k := range tracked {
		if v := os.Getenv(k); v != "" {
			snap[k] = v
		}
	}

	return snap
}

// RedactCommand strips the sudo -S password-injection syntax from a command
// string so it is safe to log. Specifically it removes " -S -p '' " from
// sudo invocations (the password itself is fed via stdin, not in the command
// string, so this is mostly cosmetic).
func RedactCommand(cmd string) string {
	return strings.ReplaceAll(cmd, " -S -p '' ", " ")
}
