package debuglog

import (
	"log/slog"
	"net/url"
	"strings"
)

// LevelTrace is a custom slog level below Debug, used for L3 (max verbosity)
// events such as per-round-trip SSH timing and raw command output.
const LevelTrace = slog.Level(-4)

// ─── Typed attr constructors ─────────────────────────────────────────────────
//
// Using constructors instead of raw slog.String("key", ...) calls at each
// log site prevents key-name typos and keeps field names consistent across
// the entire log file — important for AI-assisted analysis where a consumer
// can group by "op" or filter by "check".

// AttrsCLIInvocation returns attrs for the first log record of every run.
// argv must already be redacted via RedactArgv before passing here.
func AttrsCLIInvocation(argv []string, env map[string]string, version string) []slog.Attr {
	envAttrs := make([]any, 0, len(env)*2)
	for k, v := range env {
		envAttrs = append(envAttrs, slog.String(k, v))
	}
	return []slog.Attr{
		slog.Any("argv", argv),
		slog.String("version", version),
		slog.Group("env", envAttrs...),
	}
}

// AttrsSSHDial returns attrs logged when abc begins dialling an SSH target.
func AttrsSSHDial(host string, port int, user string, authMethods []string, jumpHost string) []slog.Attr {
	return []slog.Attr{
		slog.String("op", "node.add"),
		slog.String("host", host),
		slog.Int("port", port),
		slog.String("user", user),
		slog.Any("auth_methods", authMethods),
		slog.String("jump_host", jumpHost),
	}
}

// AttrsSSHDialOK returns attrs logged on a successful SSH connection.
func AttrsSSHDialOK(authMethodUsed string, dialMS int64) []slog.Attr {
	return []slog.Attr{
		slog.String("auth_method_used", authMethodUsed),
		slog.Int64("dial_ms", dialMS),
	}
}

// AttrsHostKey returns attrs logged when a host key is received.
func AttrsHostKey(fingerprint, algo string, knownHostsExisted bool, tofuDecision string) []slog.Attr {
	return []slog.Attr{
		slog.String("fingerprint", fingerprint),
		slog.String("algo", algo),
		slog.Bool("known_hosts_existed", knownHostsExisted),
		slog.String("tofu_decision", tofuDecision),
	}
}

// AttrsSSHCommand returns attrs logged when a command is run over SSH.
// cmd should be pre-scrubbed via RedactCommand before calling here.
func AttrsSSHCommand(cmd string, exitCode int, durationMS int64) []slog.Attr {
	return []slog.Attr{
		slog.String("command", cmd),
		slog.Int("exit_code", exitCode),
		slog.Int64("duration_ms", durationMS),
	}
}

// AttrsUpload returns attrs logged when a file is uploaded to the remote host.
func AttrsUpload(localSrc string, sizeBytes int64, remotePath, mode, method string, durationMS int64) []slog.Attr {
	return []slog.Attr{
		slog.String("src_local", localSrc),
		slog.Int64("size_bytes", sizeBytes),
		slog.String("remote_path", remotePath),
		slog.String("mode", mode),
		slog.String("method", method),
		slog.Int64("duration_ms", durationMS),
	}
}

// AttrsPreflight returns attrs logged for each individual preflight check.
func AttrsPreflight(check string, passed bool, rawOutput string, durationMS int64) []slog.Attr {
	// Trim raw output to avoid very long records; 512 chars is enough context.
	if len(rawOutput) > 512 {
		rawOutput = rawOutput[:512] + "…"
	}
	return []slog.Attr{
		slog.String("check", check),
		slog.Bool("passed", passed),
		slog.String("raw_output", strings.TrimSpace(rawOutput)),
		slog.Int64("duration_ms", durationMS),
	}
}

// AttrsPreflightSummary returns attrs for the preflight.complete record.
func AttrsPreflightSummary(run, passed, failed int, totalMS int64) []slog.Attr {
	return []slog.Attr{
		slog.Int("checks_run", run),
		slog.Int("checks_passed", passed),
		slog.Int("checks_failed", failed),
		slog.Int64("total_ms", totalMS),
	}
}

// AttrsDownload returns attrs logged for binary/asset downloads.
// The URL's query string is stripped before logging (tokens often live there).
func AttrsDownload(rawURL string, sizeBytes int64, sha256Expected, sha256Got string, verified bool, durationMS int64) []slog.Attr {
	safeURL := stripURLQuery(rawURL)
	return []slog.Attr{
		slog.String("url", safeURL),
		slog.Int64("size_bytes", sizeBytes),
		slog.String("sha256_expected", sha256Expected),
		slog.String("sha256_got", sha256Got),
		slog.Bool("sha256_verified", verified),
		slog.Int64("duration_ms", durationMS),
	}
}

// AttrsServiceOp returns attrs logged for systemd/launchd service operations.
func AttrsServiceOp(name, action, outcome string) []slog.Attr {
	return []slog.Attr{
		slog.String("service", name),
		slog.String("action", action),
		slog.String("outcome", outcome),
	}
}

// AttrsHealthPoll returns attrs logged for each Nomad agent health-check attempt.
func AttrsHealthPoll(attempt int, healthy bool, detail string) []slog.Attr {
	return []slog.Attr{
		slog.Int("attempt", attempt),
		slog.Bool("healthy", healthy),
		slog.String("detail", detail),
	}
}

// AttrsError returns attrs for an error log record.
// It walks the error chain via Unwrap to build a human-readable chain slice.
func AttrsError(op string, err error) []slog.Attr {
	if err == nil {
		return nil
	}
	chain := errorChain(err)
	return []slog.Attr{
		slog.String("op", op),
		slog.String("error", err.Error()),
		slog.Any("error_chain", chain),
	}
}

// AttrsHTTPRequest returns attrs logged when an outbound HTTP request is sent.
// The URL's query string is stripped (tokens live there).
func AttrsHTTPRequest(method, rawURL string, bodyBytes int64) []slog.Attr {
	return []slog.Attr{
		slog.String("method", method),
		slog.String("url", stripURLQuery(rawURL)),
		slog.Int64("body_bytes", bodyBytes),
	}
}

// AttrsHTTPResponse returns attrs logged when an HTTP response is received.
func AttrsHTTPResponse(method, rawURL string, statusCode int, durationMS int64) []slog.Attr {
	return []slog.Attr{
		slog.String("method", method),
		slog.String("url", stripURLQuery(rawURL)),
		slog.Int("status_code", statusCode),
		slog.Bool("ok", statusCode >= 200 && statusCode < 300),
		slog.Int64("duration_ms", durationMS),
	}
}

// AttrsJobSubmit returns attrs logged when a Nomad job is registered or planned.
func AttrsJobSubmit(action, jobID, evalID, namespace string, durationMS int64) []slog.Attr {
	return []slog.Attr{
		slog.String("action", action), // "register" | "plan" | "dispatch"
		slog.String("job_id", jobID),
		slog.String("eval_id", evalID),
		slog.String("namespace", namespace),
		slog.Int64("duration_ms", durationMS),
	}
}

// AttrsDataUpload returns attrs logged for TUS/data upload operations.
func AttrsDataUpload(filePath, endpoint string, sizeBytes int64, method string) []slog.Attr {
	return []slog.Attr{
		slog.String("file", filePath),
		slog.String("endpoint", stripURLQuery(endpoint)),
		slog.Int64("size_bytes", sizeBytes),
		slog.String("method", method), // "tus-single" | "tus-directory"
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func stripURLQuery(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// errorChain returns a slice of error message segments by walking Unwrap.
func errorChain(err error) []string {
	var chain []string
	for err != nil {
		msg := err.Error()
		// Each wrapped error typically starts with "context: ..." — split on ": "
		// to get just the outermost context label if possible.
		if idx := strings.Index(msg, ": "); idx > 0 {
			chain = append(chain, msg[:idx])
		} else {
			chain = append(chain, msg)
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
		} else {
			break
		}
	}
	return chain
}
