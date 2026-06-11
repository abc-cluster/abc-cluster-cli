package data

// workbench_stage.go — MinIO ↔ workbench home staging helpers for
// `abc data upload --workbench`, `abc data download --workbench`, and
// `abc data download --local` (resumable local download via s5cmd).
//
// Design:
//   - Both operations submit a Nomad raw_exec job pinned to the platform node
//     (aither) that runs s5cmd to copy files between MinIO and the slot's
//     persistent home directory at /data/workbench/<slot>/home/.
//   - The job runs under the user's MinIO credentials (read from
//     admin.services.minio in the active context).
//   - "after upload" staging (upload --workbench): tus upload must succeed
//     first; staging is a best-effort follow-on. If staging fails the file
//     is still in MinIO and can be re-staged later.
//   - "download to workbench" (download --workbench): source must be an S3
//     URI; the job copies the object(s) into ~/data/ in the slot home.
//
// Grove/garden upgrade path: at grove tier use JuiceFS mounts so data is
// immediately accessible without a separate staging job.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// workbenchSlotFromCtx derives the pool slot name (= workbench directory name)
// from auth.whoami in the active context. Returns an error if auth.whoami is
// absent — the user is not logged in as a pool slot.
func workbenchSlotFromCtx(actx abccfg.Context) (string, error) {
	if actx.Auth == nil || strings.TrimSpace(actx.Auth.Whoami) == "" {
		return "", fmt.Errorf(
			"--workbench requires a pool slot context (auth.whoami not set)\n" +
				"  Verify you are using the correct abc context with a valid Nomad pool token.\n" +
				"  Run: abc context list",
		)
	}
	slot := utils.WhoamiPathSegment(actx.Auth.Whoami)
	if slot == "" {
		return "", fmt.Errorf(
			"--workbench: cannot derive slot name from auth.whoami %q",
			actx.Auth.Whoami,
		)
	}
	return slot, nil
}

// workbenchS3PathForFile builds the canonical S3 path for a file that was
// (or will be) uploaded via `abc data upload`. Mirrors the path computed by
// buildRegistryFn in upload.go:
//
//	s3://<namespace>/user/<slug>/data/<filename>
func workbenchS3PathForFile(actx abccfg.Context, filename string) string {
	bucket := strings.TrimSpace(actx.NomadNamespace())
	slug := utils.WhoamiPathSegment(actx.Auth.Whoami)
	if bucket == "" || slug == "" || filename == "" {
		return ""
	}
	return fmt.Sprintf("s3://%s/user/%s/data/%s", bucket, slug, filepath.Base(filename))
}

// platformNodeName returns the Nomad node name for the platform / workbench
// node. Reads admin.services.workbench.node from config when set; defaults to
// "aither" (the seedling production node name).
func platformNodeName(actx abccfg.Context) string {
	if v, ok := abccfg.GetAdminFloorField(&actx.Admin.Services, "workbench", "node"); ok {
		if n := strings.TrimSpace(v); n != "" {
			return n
		}
	}
	return "aither"
}

// resolveWorkbenchMinioCredsFromCtx extracts MinIO endpoint + credentials from
// the active context's admin.services.minio section.  Returns an error if the
// endpoint or credentials are missing.
func resolveWorkbenchMinioCredsFromCtx(actx abccfg.Context) (endpoint, accessKey, secretKey string, err error) {
	svc := actx.Admin.Services.MinIO
	if svc != nil {
		endpoint = strings.TrimSpace(svc.Endpoint)
		if endpoint == "" {
			endpoint = strings.TrimSpace(svc.HTTP)
		}
		accessKey = strings.TrimSpace(svc.AccessKey)
		if accessKey == "" {
			accessKey = strings.TrimSpace(svc.User)
		}
		secretKey = strings.TrimSpace(svc.SecretKey)
		if secretKey == "" {
			secretKey = strings.TrimSpace(svc.Password)
		}
		if svc.CredSource != nil {
			local := svc.CredSource.Local
			if v := strings.TrimSpace(local["endpoint"]); v != "" && endpoint == "" {
				endpoint = v
			}
			if v := strings.TrimSpace(local["access_key"]); v != "" && accessKey == "" {
				accessKey = v
			}
			if v := strings.TrimSpace(local["secret_key"]); v != "" && secretKey == "" {
				secretKey = v
			}
		}
	}

	// Also try the legacy endpoint helper (covers abc-nodes clusters).
	if endpoint == "" {
		endpoint = strings.TrimSpace(actx.MinioS3APIEndpoint())
	}

	if endpoint == "" {
		return "", "", "", fmt.Errorf(
			"--workbench: admin.services.minio.endpoint not configured\n" +
				"  Run: abc config set contexts.<name>.admin.services.minio.endpoint http://<host>:9000",
		)
	}
	if accessKey == "" || secretKey == "" {
		return "", "", "", fmt.Errorf(
			"--workbench: admin.services.minio credentials not configured\n" +
				"  Run: abc config set contexts.<name>.admin.services.minio.access_key <key>",
		)
	}
	return endpoint, accessKey, secretKey, nil
}

// buildWorkbenchStageScript returns a bash script for a Nomad raw_exec job
// that copies src (S3 URI) to dst (local filesystem path on the platform node).
//
// The script:
//  1. Looks for s5cmd in PATH, then /opt/abc-seedling/nf-work/bin/s5cmd,
//     then ${NOMAD_TASK_DIR}/s5cmd (staged by Nomad artifact stanza).
//  2. Creates the destination directory.
//  3. Runs s5cmd cp.
//  4. Chowns the result to jupyter-<slot> so the JupyterLab user owns it.
func buildWorkbenchStageScript(endpoint, accessKey, secretKey, src, dst, slot string) string {
	destDir := filepath.Dir(dst)
	filename := filepath.Base(dst)
	systemUser := "jupyter-" + slot

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("export AWS_ACCESS_KEY_ID=%s\n", shellEscape(accessKey)))
	sb.WriteString(fmt.Sprintf("export AWS_SECRET_ACCESS_KEY=%s\n", shellEscape(secretKey)))
	sb.WriteString("export AWS_REGION=\"${AWS_REGION:-us-east-1}\"\n\n")

	// Locate s5cmd.
	sb.WriteString("S5CMD=$(command -v s5cmd 2>/dev/null ||\n")
	sb.WriteString("        { [ -x /opt/abc-seedling/nf-work/bin/s5cmd ] && echo /opt/abc-seedling/nf-work/bin/s5cmd; } ||\n")
	sb.WriteString("        { [ -x \"${NOMAD_TASK_DIR}/s5cmd\" ] && echo \"${NOMAD_TASK_DIR}/s5cmd\"; })\n")
	sb.WriteString("if [ -z \"$S5CMD\" ]; then\n")
	sb.WriteString("  echo 'ERROR: s5cmd not found on this node (checked PATH and /opt/abc-seedling/nf-work/bin/)' >&2\n")
	sb.WriteString("  exit 1\n")
	sb.WriteString("fi\n\n")

	sb.WriteString(fmt.Sprintf("mkdir -p %s\n\n", shellEscape(destDir)))

	sb.WriteString("echo '── staging from MinIO to workbench ──'\n")
	sb.WriteString(fmt.Sprintf("echo '   src : %s'\n", src))
	sb.WriteString(fmt.Sprintf("echo '   dst : %s'\n", dst))
	sb.WriteString(fmt.Sprintf("echo '   slot: %s'\n\n", slot))

	sb.WriteString(fmt.Sprintf(`"$S5CMD" --endpoint-url %s cp %s %s`+"\n",
		shellEscape(endpoint), shellEscape(src), shellEscape(dst),
	))

	// Chown to the slot user so JupyterLab can read/write.
	sb.WriteString(fmt.Sprintf("\nif id %s &>/dev/null; then\n", shellEscape(systemUser)))
	sb.WriteString(fmt.Sprintf("  chown %s:%s %s\n",
		shellEscape(systemUser), shellEscape(systemUser), shellEscape(dst)),
	)
	sb.WriteString("fi\n\n")

	sb.WriteString(fmt.Sprintf("echo '✓ staged: %s/%s'\n", destDir, filename))
	return sb.String()
}

// buildWorkbenchStageScriptMulti returns a script that copies all objects
// matching an S3 prefix (trailing /*) into a workbench destination directory.
// Used when --workbench is given with a directory source.
func buildWorkbenchStageScriptMulti(endpoint, accessKey, secretKey, srcPrefix, dstDir, slot string) string {
	systemUser := "jupyter-" + slot

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("export AWS_ACCESS_KEY_ID=%s\n", shellEscape(accessKey)))
	sb.WriteString(fmt.Sprintf("export AWS_SECRET_ACCESS_KEY=%s\n", shellEscape(secretKey)))
	sb.WriteString("export AWS_REGION=\"${AWS_REGION:-us-east-1}\"\n\n")

	sb.WriteString("S5CMD=$(command -v s5cmd 2>/dev/null ||\n")
	sb.WriteString("        { [ -x /opt/abc-seedling/nf-work/bin/s5cmd ] && echo /opt/abc-seedling/nf-work/bin/s5cmd; } ||\n")
	sb.WriteString("        { [ -x \"${NOMAD_TASK_DIR}/s5cmd\" ] && echo \"${NOMAD_TASK_DIR}/s5cmd\"; })\n")
	sb.WriteString("if [ -z \"$S5CMD\" ]; then\n")
	sb.WriteString("  echo 'ERROR: s5cmd not found on this node' >&2; exit 1\n")
	sb.WriteString("fi\n\n")

	sb.WriteString(fmt.Sprintf("mkdir -p %s\n\n", shellEscape(dstDir)))

	sb.WriteString(fmt.Sprintf(`"$S5CMD" --endpoint-url %s cp %s %s/`+"\n",
		shellEscape(endpoint), shellEscape(strings.TrimRight(srcPrefix, "/")+"/*"), shellEscape(dstDir),
	))

	sb.WriteString(fmt.Sprintf("\nif id %s &>/dev/null; then\n", shellEscape(systemUser)))
	sb.WriteString(fmt.Sprintf("  chown -R %s:%s %s\n",
		shellEscape(systemUser), shellEscape(systemUser), shellEscape(dstDir)),
	)
	sb.WriteString("fi\n\n")

	sb.WriteString(fmt.Sprintf("echo '✓ staged to workbench: %s/'\n", dstDir))
	return sb.String()
}

// stageToWorkbench submits a Nomad raw_exec job pinned to the platform node
// that copies src (S3 URI) to dst (workbench filesystem path).
// Returns nil on successful job submission; the job is async — check `abc job logs`
// for the copy result.
func stageToWorkbench(cmd *cobra.Command, actx abccfg.Context, src, dst, slot string) error {
	endpoint, accessKey, secretKey, err := resolveWorkbenchMinioCredsFromCtx(actx)
	if err != nil {
		return err
	}

	script := buildWorkbenchStageScript(endpoint, accessKey, secretKey, src, dst, slot)
	platformNode := platformNodeName(actx)

	opts := dataNomadScriptOpts{
		RunName: "data-workbench-stage",
		Driver:  "raw_exec",
		ExtraConstraints: []string{
			fmt.Sprintf("node.unique.name==%s", platformNode),
		},
	}

	// Attempt to stage s5cmd via Nomad artifact stanza if tools.toml is
	// configured.  Falls back to PATH and known local paths in the script.
	if artURL := toolNomadArtifactURL("s5cmd"); artURL != "" {
		opts.Artifacts = []downloadArtifact{{URL: artURL, Dest: "local/"}}
	}

	return submitDataNomadScript(cmd, opts, script)
}

// StageS3ToWorkbench is the exported entry point for "download --workbench":
// copies an S3 object (or prefix) to the user's workbench ~/data/ directory.
// src must be an S3 URI (s3://...).  If src ends with "/" it is treated as a
// prefix (multi-object copy).
func StageS3ToWorkbench(cmd *cobra.Command, src string) error {
	cfg, err := abccfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()

	slot, err := workbenchSlotFromCtx(actx)
	if err != nil {
		return err
	}

	if !strings.HasPrefix(strings.ToLower(src), "s3://") {
		return fmt.Errorf("--workbench: source must be an S3 URI (s3://...); got %q", src)
	}

	endpoint, accessKey, secretKey, err := resolveWorkbenchMinioCredsFromCtx(actx)
	if err != nil {
		return err
	}

	platformNode := platformNodeName(actx)
	dstDir := fmt.Sprintf("/data/workbench/%s/home/data", slot)

	var script string
	if strings.HasSuffix(src, "/") {
		script = buildWorkbenchStageScriptMulti(endpoint, accessKey, secretKey, src, dstDir, slot)
	} else {
		filename := filepath.Base(src)
		dst := dstDir + "/" + filename
		script = buildWorkbenchStageScript(endpoint, accessKey, secretKey, src, dst, slot)
	}

	opts := dataNomadScriptOpts{
		RunName: "data-workbench-stage",
		Driver:  "raw_exec",
		ExtraConstraints: []string{
			fmt.Sprintf("node.unique.name==%s", platformNode),
		},
	}
	if artURL := toolNomadArtifactURL("s5cmd"); artURL != "" {
		opts.Artifacts = []downloadArtifact{{URL: artURL, Dest: "local/"}}
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Staging to workbench slot %q → %s/...\n", slot, dstDir)
	return submitDataNomadScript(cmd, opts, script)
}

// LocalFetchFromS3 downloads an S3 object (or prefix) to the user's local
// machine by shelling out to s5cmd.  This is for `abc data download --local`,
// not for staging to the workbench.
//
// Why s5cmd rather than the existing tus upload flow? s5cmd supports resumable
// multi-part downloads (--if-size-differ, checksum-aware), handles large files
// efficiently, and is already a first-class tool in the abc-cluster toolchain.
//
// s5cmd is located by checking (in order):
//  1. $PATH
//  2. /opt/abc-seedling/nf-work/bin/s5cmd  (known install path on platform node, also
//     useful when abc is invoked via SSH)
//
// Returns an error with a helpful message if s5cmd is not found.
func LocalFetchFromS3(cmd *cobra.Command, src, dstDir string, parallel int) error {
	if !strings.HasPrefix(strings.ToLower(src), "s3://") {
		return fmt.Errorf("--local: source must be an S3 URI (s3://...); got %q", src)
	}

	cfg, err := abccfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()

	endpoint, accessKey, secretKey, err := resolveWorkbenchMinioCredsFromCtx(actx)
	if err != nil {
		return err
	}

	// Locate s5cmd.
	s5cmdPath, err := findLocalS5cmd()
	if err != nil {
		return err
	}

	if dstDir == "" {
		// Default to the current working directory.
		if dstDir, err = os.Getwd(); err != nil {
			return fmt.Errorf("cannot determine working directory: %w", err)
		}
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("cannot create destination directory %q: %w", dstDir, err)
	}

	if parallel <= 0 {
		parallel = 4
	}

	// For a prefix copy, ensure the source ends with /* so s5cmd copies contents.
	cpSrc := src
	if strings.HasSuffix(src, "/") {
		cpSrc = strings.TrimRight(src, "/") + "/*"
	}
	cpDst := dstDir
	if !strings.HasSuffix(cpDst, "/") {
		cpDst += "/"
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Downloading %s → %s (s5cmd, %d workers)...\n", src, dstDir, parallel)

	s5cmdArgs := []string{
		"--endpoint-url", endpoint,
		"--numworkers", fmt.Sprintf("%d", parallel),
		"cp",
		// s5cmd cp --if-size-differ skips objects whose size already matches the
		// destination, giving resumable, idempotent re-runs. (s5cmd has no
		// checksum-based skip flag; --if-size-differ is the correct one — see
		// `s5cmd cp --help`. The symmetric LocalPushToS3 uses the same flag.)
		"--if-size-differ",
		cpSrc,
		cpDst,
	}

	c := exec.CommandContext(cmd.Context(), s5cmdPath, s5cmdArgs...)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	c.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID="+accessKey,
		"AWS_SECRET_ACCESS_KEY="+secretKey,
		"AWS_REGION=us-east-1",
	)

	if err := c.Run(); err != nil {
		return fmt.Errorf("s5cmd download failed: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✓ downloaded to %s\n", dstDir)
	return nil
}

// LocalPushToS3 uploads a local file (or directory) from the user's machine to
// an S3 destination by shelling out to s5cmd. It is the symmetric counterpart of
// LocalFetchFromS3 and is used by the `abc job run` data-staging seam to upload
// local-only inputs to the per-run inputs prefix before the job is submitted.
//
// Endpoint + credentials are resolved from the active context the same way as
// LocalFetchFromS3 (resolveWorkbenchMinioCredsFromCtx); s5cmd is located via
// findLocalS5cmd ($PATH, then /opt/abc-seedling/nf-work/bin/s5cmd).
//
// Returns an error with a helpful message if s5cmd is not found or the transfer
// fails. The caller (job-staging) treats any error as fatal — a job whose
// stage-in would 404 must not be submitted.
func LocalPushToS3(cmd *cobra.Command, src, s3dest string, parallel int) error {
	if !strings.HasPrefix(strings.ToLower(s3dest), "s3://") {
		return fmt.Errorf("push: destination must be an S3 URI (s3://...); got %q", s3dest)
	}

	cfg, err := abccfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()

	endpoint, accessKey, secretKey, err := resolveWorkbenchMinioCredsFromCtx(actx)
	if err != nil {
		return err
	}

	s5cmdPath, err := findLocalS5cmd()
	if err != nil {
		return err
	}

	if parallel <= 0 {
		parallel = 4
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Uploading %s → %s (s5cmd, %d workers)...\n", src, s3dest, parallel)

	s5cmdArgs := []string{
		"--endpoint-url", endpoint,
		"--numworkers", fmt.Sprintf("%d", parallel),
		"cp",
		// --if-size-differ skips re-uploading objects whose size already
		// matches, giving cheap idempotent re-runs of a staged job.
		"--if-size-differ",
		src,
		s3dest,
	}

	c := exec.CommandContext(cmd.Context(), s5cmdPath, s5cmdArgs...)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	c.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID="+accessKey,
		"AWS_SECRET_ACCESS_KEY="+secretKey,
		"AWS_REGION=us-east-1",
	)

	if err := c.Run(); err != nil {
		return fmt.Errorf("s5cmd upload failed: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✓ uploaded to %s\n", s3dest)
	return nil
}

// LocalPushBytesToS3 writes data to a temp file and uploads it to an S3
// destination via LocalPushToS3. Used to push the run-manifest.json lineage
// record to the per-run prefix without the caller managing a temp file.
func LocalPushBytesToS3(cmd *cobra.Command, data []byte, s3dest string) error {
	tmp, err := os.CreateTemp("", "abc-push-*")
	if err != nil {
		return fmt.Errorf("create temp file for push: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file for push: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file for push: %w", err)
	}
	return LocalPushToS3(cmd, tmpPath, s3dest, 1)
}

// findLocalS5cmd returns the path to a local s5cmd binary, checking $PATH first
// then /opt/abc-seedling/nf-work/bin/s5cmd. Returns an error with install guidance
// if not found.
func findLocalS5cmd() (string, error) {
	if p, err := exec.LookPath("s5cmd"); err == nil {
		return p, nil
	}
	const fallback = "/opt/abc-seedling/nf-work/bin/s5cmd"
	if _, err := os.Stat(fallback); err == nil {
		return fallback, nil
	}
	return "", fmt.Errorf(
		"s5cmd not found in PATH or %s\n"+
			"  Install it from https://github.com/peak/s5cmd/releases or ask your cluster operator\n"+
			"  to add it to PATH (run `abc admin tools push` to register the cluster's binary)",
		fallback,
	)
}
