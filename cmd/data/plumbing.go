package data

// plumbing.go — shared infrastructure for abc data plumbing commands.
//
// Plumbing commands (list, copy, move, remove, sync, cat, pipe, disk-usage,
// make-bucket, remove-bucket, stat, run, select) are thin wrappers over local
// tools (s5cmd, mcli, aria2c, rclone). They do not re-implement S3 operations.
//
// Each plumbing command:
//   1. Resolves credentials + endpoint from the active context.
//   2. Locates the tool binary via findTool().
//   3. Constructs the tool invocation.
//   4. Calls execTool() which execs the binary, streaming stdout/stderr.
//
// Tool binary resolution order (findTool):
//   1. ABC_BIN_<TOOL> environment variable (e.g. ABC_BIN_S5CMD)
//   2. ~/.abc/binaries/<tool>  — managed by `abc admin tools fetch`
//   3. exec.LookPath(tool)     — system PATH
//   4. Error with install instructions
//
// For mcli: also checks for "mc" binary but verifies it is MinIO Client
// (not GNU Midnight Commander, which has the same name on Ubuntu).

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/credsource"
	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
)

// installURLs maps tool names to their install documentation URLs.
var installURLs = map[string]string{
	"s5cmd":  "https://github.com/peak/s5cmd/releases",
	"mcli":   "https://min.io/docs/minio/linux/reference/minio-mc.html",
	"mc":     "https://min.io/docs/minio/linux/reference/minio-mc.html",
	"aria2c": "https://github.com/aria2/aria2/releases",
	"rclone": "https://rclone.org/install/",
}

// toolEnvVar maps a tool name to its registered ABC_BIN_<TOOL> environment variable.
// Convention from envvars/registry.go: ABC_BIN_<TOOL> (BucketToolBinary).
var toolEnvVar = map[string]string{
	"s5cmd":  "ABC_BIN_S5CMD",
	"rclone": "ABC_BIN_RCLONE",
	"mcli":   "ABC_BIN_MCLI",
	"mc":     "ABC_BIN_MC",
	"aria2c": "ABC_BIN_ARIA2C",
}

// findTool resolves the path to a named binary using the following priority:
//
//  1. ABC_<TOOL>_BIN environment variable override (registered in envvars registry)
//     e.g. ABC_BIN_S5CMD=/opt/bin/s5cmd
//  2. ~/.abc/binaries/<tool> — fetched and managed by `abc admin tools`
//  3. exec.LookPath(tool)   — system PATH
//
// For "mcli": also checks ABC_BIN_MC, ~/.abc/binaries/mc, and mc on PATH,
// verifying it is MinIO Client (not GNU Midnight Commander) before accepting.
//
// Returns an error with install instructions if the tool cannot be found.
func findTool(name string) (string, error) {
	// 1. Environment variable override (ABC_<TOOL>_BIN).
	envKey := toolEnvVar[name]
	if envKey != "" {
		if v := strings.TrimSpace(envvars.Get(envKey)); v != "" {
			if isExecutable(v) {
				return v, nil
			}
			return "", fmt.Errorf("%s=%q: file not found or not executable", envKey, v)
		}
	}

	// 2. ~/.abc/binaries/<name> — managed by `abc admin tools fetch`.
	if managedPath, err := utils.ManagedBinaryPath(name); err == nil {
		if isExecutable(managedPath) {
			return managedPath, nil
		}
	}

	// 3. System PATH.
	if p, err := exec.LookPath(name); err == nil {
		// For mcli: the managed name is "mcli" but the system might have "mcli"
		// already. Accept it — it's either mcli or it will be verified below.
		return p, nil
	}

	// 4. For mcli: also accept "mc" from ABC_BIN_MC, ~/.abc/binaries/mc, or PATH.
	if name == "mcli" {
		// Check ABC_BIN_MC env var.
		if mcBin := strings.TrimSpace(envvars.Get("ABC_BIN_MC")); mcBin != "" {
			if isExecutable(mcBin) && isMinioClient(mcBin) {
				return mcBin, nil
			}
		}
		// Check ~/.abc/binaries/mc.
		if managedPath, err := utils.ManagedBinaryPath("mc"); err == nil {
			if isExecutable(managedPath) && isMinioClient(managedPath) {
				return managedPath, nil
			}
		}
		// Check PATH.
		if p, err := exec.LookPath("mc"); err == nil {
			if isMinioClient(p) {
				return p, nil
			}
		}
	}

	// Not found — return install instructions.
	url := installURLs[name]
	if url == "" {
		url = "https://github.com/search?q=" + name
	}
	return "", fmt.Errorf(
		"%s not found.\n\n"+
			"Install from: %s\n\n"+
			"Or let abc manage it:\n"+
			"  abc admin tools fetch %s   # download to ~/.abc/binaries/%s\n\n"+
			"Or set %s=/path/to/%s to override the lookup.",
		name, url, name, name, envKey, name,
	)
}

// isExecutable reports whether path exists and is an executable file.
func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// isMinioClient runs <binary> --version and checks that the output mentions
// "minio" or "mc version" — distinguishing MinIO Client from GNU Midnight
// Commander (which also installs as "mc" on Ubuntu).
func isMinioClient(binary string) bool {
	out, err := exec.Command(binary, "--version").Output()
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(out))
	return strings.Contains(lower, "minio") || strings.Contains(lower, "mc version")
}

// ResolveS3Creds reads S3 credentials and endpoint from the active context
// using a universal resolution that works for all cluster types (abc-nodes,
// abc-seedling, abc-cloud). It is the one cred resolver for the CLI; the job
// data-staging seam (cmd/job) calls it to populate the stage tasks' env.
//
// Phase 1.5b: when the context has cred_source set to a broker tier
// (seedling/v1 etc.), credentials are obtained from the credential broker
// rather than from the on-disk fields — those are deliberately empty for
// opaque-shape configs. The broker's returned MinIO bundle wins; if the
// broker fails we fall through to the legacy local-fields path.
//
// Resolution order within each service (when not broker-routed):
//
//	admin.services.minio.{endpoint,access_key,secret_key}  (preferred)
//	admin.services.rustfs.{endpoint,access_key,secret_key} (fallback)
//	admin.abc_nodes.{s3_endpoint,s3_access_key,s3_secret_key} (legacy)
func ResolveS3Creds(ctx abccfg.Context) (endpoint, accessKey, secretKey string) {
	if credsource.IsBroker(ctx.CredSource) {
		if creds, err := credsource.ResolveFromContext(context.Background(), ctx); err == nil {
			ep := strings.TrimRight(strings.TrimSpace(creds.Minio.Endpoint), "/")
			ak := strings.TrimSpace(creds.Minio.AccessKey)
			sk := strings.TrimSpace(creds.Minio.SecretKey)
			if ep != "" && ak != "" && sk != "" {
				return ep, ak, sk
			}
		} else {
			fmt.Fprintf(os.Stderr, "abc data: cred_source=%q broker resolve failed: %v\n", ctx.CredSource, err)
			// Fall through to local — for a true seedling/v1 slot the
			// admin.services.minio fields are empty by design, so the
			// downstream "minio not configured" message will follow,
			// which is the correct visible failure mode.
		}
	}
	for _, svc := range []string{"minio", "rustfs"} {
		ep, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, svc, "endpoint")
		if ep == "" {
			ep, _ = abccfg.GetAdminFloorField(&ctx.Admin.Services, svc, "http")
		}
		ak, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, svc, "access_key")
		if ak == "" {
			ak, _ = abccfg.GetAdminFloorField(&ctx.Admin.Services, svc, "user")
		}
		sk, _ := abccfg.GetAdminFloorField(&ctx.Admin.Services, svc, "secret_key")
		if sk == "" {
			sk, _ = abccfg.GetAdminFloorField(&ctx.Admin.Services, svc, "password")
		}
		if ep != "" && ak != "" && sk != "" {
			return strings.TrimRight(ep, "/"), ak, sk
		}
	}
	// Legacy abc_nodes fields.
	if n := ctx.Admin.ABCNodes; n != nil {
		ep := strings.TrimRight(n.S3Endpoint, "/")
		ak := n.S3AccessKey
		if ak == "" {
			ak = n.MinioRootUser
		}
		sk := n.S3SecretKey
		if sk == "" {
			sk = n.MinioRootPassword
		}
		return ep, ak, sk
	}
	return "", "", ""
}

// s3Env returns the OS environment with S3 credentials injected from the
// active context. Does not mutate os.Environ() — returns a new slice.
// Sets:
//
//	AWS_ACCESS_KEY_ID
//	AWS_SECRET_ACCESS_KEY
//	AWS_REGION (us-east-1 if not already set)
func s3Env(ctx abccfg.Context) []string {
	_, ak, sk := ResolveS3Creds(ctx)

	toSet := map[string]string{
		"AWS_ACCESS_KEY_ID":     ak,
		"AWS_SECRET_ACCESS_KEY": sk,
		"AWS_REGION":            "us-east-1",
	}

	env := os.Environ()
	filtered := make([]string, 0, len(env))
	overridden := make(map[string]bool)
	for _, e := range env {
		idx := strings.IndexByte(e, '=')
		if idx < 0 {
			filtered = append(filtered, e)
			continue
		}
		key := e[:idx]
		if v, ok := toSet[key]; ok {
			if v != "" {
				filtered = append(filtered, key+"="+v)
				overridden[key] = true
			}
			continue
		}
		filtered = append(filtered, e)
	}
	for k, v := range toSet {
		if !overridden[k] && v != "" {
			filtered = append(filtered, k+"="+v)
		}
	}
	return filtered
}

// s5cmdEndpoint returns the S3 endpoint from the active context for use in
// s5cmd --endpoint-url. Works for all cluster types.
func s5cmdEndpoint(ctx abccfg.Context) string {
	ep, _, _ := ResolveS3Creds(ctx)
	return ep
}

// s5cmdArgs constructs the s5cmd argument list. Global flags (e.g.
// --numworkers, --no-sign-request) that must precede the subcommand are
// passed via globalFlags. --endpoint-url is always prepended from the context.
//
//	s5cmdArgs(ctx, "ls", nil, []string{"s3://bucket/"})
//	→ ["--endpoint-url", "http://localhost:9000", "ls", "s3://bucket/"]
//
//	s5cmdArgs(ctx, "cp", []string{"--numworkers", "8"}, []string{"src", "dst"})
//	→ ["--endpoint-url", "http://...", "--numworkers", "8", "cp", "src", "dst"]
func s5cmdArgs(ctx abccfg.Context, subcmd string, rest []string, globalFlags ...string) []string {
	ep := s5cmdEndpoint(ctx)
	args := make([]string, 0, 2+len(globalFlags)+1+len(rest))
	if ep != "" {
		args = append(args, "--endpoint-url", ep)
	}
	args = append(args, globalFlags...)
	args = append(args, subcmd)
	args = append(args, rest...)
	return args
}

// mcliAlias sets up an ephemeral mcli alias in a temp directory and returns
// the alias name, the temp dir, and a cleanup function.
//
// Usage:
//
//	alias, tmpDir, cleanup, err := mcliAlias(ctx)
//	defer cleanup()
//	// use MCLI_CONFIG_DIR=tmpDir mcli <alias>/bucket/...
func mcliAlias(ctx abccfg.Context) (bin, alias, tmpDir string, cleanup func(), err error) {
	ep, ak, sk := ResolveS3Creds(ctx)

	if ep == "" || ak == "" || sk == "" {
		return "", "", "", func() {}, fmt.Errorf("no S3 endpoint or credentials in active context")
	}

	resolvedBin, err := findTool("mcli")
	if err != nil {
		return "", "", "", func() {}, err
	}

	tmp, err := os.MkdirTemp("", "abc-mcli-*")
	if err != nil {
		return "", "", "", func() {}, fmt.Errorf("create mcli temp dir: %w", err)
	}

	cleanup = func() { os.RemoveAll(tmp) }

	// Create the alias — mcli stores config in MCLI_CONFIG_DIR.
	// Use the resolved binary path (may be "mc" on some systems).
	c := exec.Command(resolvedBin, "alias", "set", "abcctx", ep, ak, sk, "--quiet")
	c.Env = append(os.Environ(), "MCLI_CONFIG_DIR="+tmp)
	if out, err := c.CombinedOutput(); err != nil {
		cleanup()
		return "", "", "", func() {}, fmt.Errorf("mcli alias set: %w\n%s", err, out)
	}
	return resolvedBin, "abcctx", tmp, cleanup, nil
}

// rcloneConf writes a minimal rclone.conf with an S3 backend configured from
// the active context and returns the file path and a cleanup function.
func rcloneConf(ctx abccfg.Context) (confPath string, cleanup func(), err error) {
	ep, ak, sk := ResolveS3Creds(ctx)

	if ep == "" || ak == "" || sk == "" {
		return "", func() {}, fmt.Errorf("no S3 endpoint or credentials in active context")
	}

	f, err := os.CreateTemp("", "abc-rclone-*.conf")
	if err != nil {
		return "", func() {}, fmt.Errorf("create rclone conf: %w", err)
	}
	cleanup = func() { os.Remove(f.Name()) }

	content := fmt.Sprintf(`[_abc]
type = s3
provider = Minio
endpoint = %s
access_key_id = %s
secret_access_key = %s
region = us-east-1
`, ep, ak, sk)

	if _, err := io.WriteString(f, content); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write rclone conf: %w", err)
	}
	f.Close()
	return f.Name(), cleanup, nil
}

// execTool runs binary with args and env, streaming stdout and stderr directly
// to the caller's stdout and stderr. Returns the tool's exit code wrapped as
// an error (nil on exit 0). The Go process does not buffer the output.
func execTool(binary string, args []string, env []string) error {
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Suppress the redundant "exit status N" wrapper when the tool itself
		// already printed an error message to stderr.
		if _, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%s exited with error", filepath.Base(binary))
		}
		return err
	}
	return nil
}

// toolInstallHint returns a short one-line install hint for a tool name,
// suitable for inclusion in error messages.
func toolInstallHint(name string) string {
	url, ok := installURLs[name]
	if !ok {
		return fmt.Sprintf("install %s and ensure it is in PATH or ~/.abc/binaries/", name)
	}
	return fmt.Sprintf("install from %s or run: abc admin tools fetch %s", url, name)
}
