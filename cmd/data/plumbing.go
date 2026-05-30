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
//   1. ABC_<TOOL>_PATH environment variable (e.g. ABC_S5CMD_PATH)
//   2. ~/.abc/binaries/<tool>  — managed by `abc admin tools fetch`
//   3. exec.LookPath(tool)     — system PATH
//   4. Error with install instructions
//
// For mcli: also checks for "mc" binary but verifies it is MinIO Client
// (not GNU Midnight Commander, which has the same name on Ubuntu).

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
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

// findTool resolves the path to a named binary using the following priority:
//
//  1. ABC_<TOOL>_PATH environment variable override
//     (e.g. ABC_S5CMD_PATH=/opt/bin/s5cmd)
//  2. ~/.abc/binaries/<tool> — fetched and managed by `abc admin tools`
//  3. exec.LookPath(tool)   — system PATH
//
// For "mcli": also checks for an "mc" binary on PATH and verifies it is
// MinIO Client (not GNU Midnight Commander) before accepting it.
//
// Returns an error with install instructions if the tool cannot be found.
func findTool(name string) (string, error) {
	// 1. Environment variable override.
	envKey := "ABC_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_PATH"
	if v := strings.TrimSpace(envvars.Get(envKey)); v != "" {
		if isExecutable(v) {
			return v, nil
		}
		return "", fmt.Errorf("%s=%q: file not found or not executable", envKey, v)
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

	// 4. For mcli: also accept "mc" from PATH if it is MinIO Client.
	if name == "mcli" {
		if p, err := exec.LookPath("mc"); err == nil {
			if isMinioClient(p) {
				return p, nil
			}
		}
		// Also check ~/.abc/binaries/mc.
		if managedPath, err := utils.ManagedBinaryPath("mc"); err == nil {
			if isExecutable(managedPath) && isMinioClient(managedPath) {
				return managedPath, nil
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

// s3Env returns the OS environment with S3 credentials injected from the
// active context. Does not mutate os.Environ() — returns a new slice.
// Sets:
//
//	AWS_ACCESS_KEY_ID
//	AWS_SECRET_ACCESS_KEY
//	AWS_REGION (us-east-1 if not already set)
func s3Env(ctx config.Context) []string {
	env := os.Environ()

	envMap := ctx.AbcNodesMinioStorageCLIEnv()
	if len(envMap) == 0 {
		envMap = ctx.AbcNodesRustfsStorageCLIEnv()
	}

	toSet := map[string]string{
		"AWS_ACCESS_KEY_ID":     envMap["AWS_ACCESS_KEY_ID"],
		"AWS_SECRET_ACCESS_KEY": envMap["AWS_SECRET_ACCESS_KEY"],
		"AWS_REGION":            "us-east-1",
	}

	// Override any existing values.
	filtered := make([]string, 0, len(env))
	overridden := make(map[string]bool)
	for _, e := range env {
		key := e[:strings.IndexByte(e, '=')]
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

// s5cmdEndpoint returns the MinIO/rustfs S3 endpoint from the active context.
// Prefers rustfs; falls back to MinIO.
func s5cmdEndpoint(ctx config.Context) string {
	if ep := strings.TrimRight(ctx.RustfsS3APIEndpoint(), "/"); ep != "" {
		return ep
	}
	return strings.TrimRight(ctx.MinioS3APIEndpoint(), "/")
}

// s5cmdArgs constructs the s5cmd argument list: prepends the global
// --endpoint-url flag before the subcommand and user-provided args.
//
//	s5cmdArgs(ctx, "ls", []string{"s3://bucket/"})
//	→ ["--endpoint-url", "http://localhost:9000", "ls", "s3://bucket/"]
func s5cmdArgs(ctx config.Context, subcmd string, rest []string) []string {
	ep := s5cmdEndpoint(ctx)
	args := make([]string, 0, 2+1+len(rest))
	if ep != "" {
		args = append(args, "--endpoint-url", ep)
	}
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
func mcliAlias(ctx config.Context) (alias, tmpDir string, cleanup func(), err error) {
	ep := s5cmdEndpoint(ctx) // mcli uses the same endpoint
	envMap := ctx.AbcNodesMinioStorageCLIEnv()
	if len(envMap) == 0 {
		envMap = ctx.AbcNodesRustfsStorageCLIEnv()
	}
	ak := envMap["AWS_ACCESS_KEY_ID"]
	sk := envMap["AWS_SECRET_ACCESS_KEY"]

	if ep == "" || ak == "" || sk == "" {
		return "", "", func() {}, fmt.Errorf("no S3 endpoint or credentials in active context")
	}

	tmp, err := os.MkdirTemp("", "abc-mcli-*")
	if err != nil {
		return "", "", func() {}, fmt.Errorf("create mcli temp dir: %w", err)
	}

	cleanup = func() { os.RemoveAll(tmp) }

	// Create the alias — mcli stores config in MCLI_CONFIG_DIR.
	cmd := exec.Command("mcli", "alias", "set", "_abc", ep, ak, sk, "--quiet")
	cmd.Env = append(os.Environ(), "MCLI_CONFIG_DIR="+tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", "", func() {}, fmt.Errorf("mcli alias set: %w\n%s", err, out)
	}
	return "_abc", tmp, cleanup, nil
}

// rcloneConf writes a minimal rclone.conf with an S3 backend configured from
// the active context and returns the file path and a cleanup function.
func rcloneConf(ctx config.Context) (confPath string, cleanup func(), err error) {
	ep := s5cmdEndpoint(ctx)
	envMap := ctx.AbcNodesMinioStorageCLIEnv()
	if len(envMap) == 0 {
		envMap = ctx.AbcNodesRustfsStorageCLIEnv()
	}
	ak := envMap["AWS_ACCESS_KEY_ID"]
	sk := envMap["AWS_SECRET_ACCESS_KEY"]

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
