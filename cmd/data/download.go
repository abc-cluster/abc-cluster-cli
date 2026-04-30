package data

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/api"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	tomlparse "github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type downloadOptions struct {
	runName     string
	accessions  []string
	configFile  string
	paramsFile  string
	profile     string
	workDir     string
	revision    string
	tool        string
	driver      string
	source      string
	destination string
	storagePath string // overrides auto-derived s3:// path when --destination storage
	urlFile     string
	parallel    int
	toolArgs    string

	// placementNode is a Nomad node ID (UUID) or node name; adds a placement constraint to the generated job script.
	placementNode string
}

const defaultDockerImage = "ghcr.io/abc-cluster/abc-data-transfer:v2026-01-01"

var dockerImageByTool = map[string]string{
	"aria2":    "quay.io/biocontainers/aria2:1.36.0",
	"rclone":   "quay.io/rclone/rclone:1.77.0",
	"wget":     "busybox:1.36.0",
	"s5cmd":    "quay.io/s5cmd/s5cmd:2.1.0",
	"nextflow": "nextflow/nextflow:25.10.4",
}

func newDownloadCmd(serverURL, accessToken, workspace *string, factory PipelineClientFactory) *cobra.Command {
	opts := &downloadOptions{}

	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download data via various tools",
		Long: `Download data via selected tool and dispatch as Nomad job.

Supports driver selection (exec, raw_exec, docker, or containerd) with a pinned OCI image when using docker or containerd; Nomad registers Roblox nomad-driver-containerd as task driver "containerd-driver" (the CLI maps --driver=containerd to that name).

Use --destination for the directory on the task filesystem where files are written.
Use --destination storage to push files directly to the cluster's S3 backend (rustfs or MinIO), resolved from the active context's admin.services config.
Use --node to pin the job to a specific Nomad node (node UUID or node name).
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDownload(cmd, opts, *serverURL, *accessToken, *workspace, factory)
		},
	}

	cmd.Flags().StringVar(&opts.runName, "name", "", "custom job name")
	cmd.Flags().StringSliceVar(&opts.accessions, "accession", nil, "accession(s) for nextflow")
	cmd.Flags().StringVar(&opts.configFile, "config", "", "nextflow config file")
	cmd.Flags().StringVar(&opts.paramsFile, "params-file", "", "nextflow params file")
	cmd.Flags().StringVar(&opts.profile, "profile", "", "nextflow profile")
	cmd.Flags().StringVar(&opts.workDir, "work-dir", "", "nextflow work dir")
	cmd.Flags().StringVar(&opts.revision, "revision", "", "nextflow revision tag/commit")

	cmd.Flags().StringVar(&opts.tool, "tool", "aria2", "download tool: aria2,rclone,wget,s5cmd,nextflow")
	cmd.Flags().StringVar(&opts.driver, "driver", "exec", "nomad driver: exec, raw_exec, docker, or containerd (oci drivers need image; containerd → Nomad task driver containerd-driver)")
	cmd.Flags().StringVar(&opts.source, "source", "", "source URL/path or s3:// URI")
	cmd.Flags().StringVar(&opts.destination, "destination", "",
		"destination path on the task filesystem, an s3:// URI, or the special value 'storage' to push "+
			"to the cluster's S3 backend (rustfs/MinIO) under downloads/<namespace>/<user>/")
	cmd.Flags().StringVar(&opts.storagePath, "storage-path", "",
		"override the S3 path used with --destination storage; accepts a full s3://bucket/prefix/ URI "+
			"or a relative path appended to the default downloads/<namespace>/<user>/ base")
	cmd.Flags().StringVar(&opts.placementNode, "node", "", "Nomad node to run the download job on (node UUID or node name; adds a placement constraint)")
	cmd.Flags().StringVar(&opts.urlFile, "url-file", "", "newline-separated URL file")
	cmd.Flags().IntVar(&opts.parallel, "parallel", 4, "parallelism")
	cmd.Flags().StringVar(&opts.toolArgs, "tool-args", "", "extra flags passed to the download tool (for s5cmd, these become global flags before the subcommand, e.g. --tool-args='--no-sign-request')")

	return cmd
}

func runDownload(cmd *cobra.Command, opts *downloadOptions, serverURL, accessToken, workspace string, factory PipelineClientFactory) error {
	if opts.tool == "" {
		opts.tool = "aria2"
	}
	if opts.driver == "" {
		opts.driver = "exec"
	}

	tool := strings.ToLower(opts.tool)
	driver := strings.ToLower(opts.driver)

	// raw_exec is the default for s5cmd: it runs directly on the node without a
	// container, relying on the Nomad artifact stanza to stage the s5cmd binary.
	if tool == "s5cmd" && !cmd.Flags().Changed("driver") {
		driver = "raw_exec"
		opts.driver = "raw_exec"
	}

	if tool != "nextflow" {
		if driver != "exec" && driver != "raw_exec" && driver != "docker" && driver != "containerd" && driver != "containerd-driver" {
			return fmt.Errorf("unsupported driver %q (use exec, raw_exec, docker, or containerd)", driver)
		}
		if (driver == "docker" || driver == "containerd" || driver == "containerd-driver") && opts.destination == "" {
			opts.destination = "/tmp/abc-data-download"
		}
		uploadEndpoint, err := resolveUploadEndpoint(cmd, "", serverURL)
		if err != nil {
			return err
		}
		uploadToken := resolveUploadToken(cmd, "", accessToken)

		if strings.TrimSpace(opts.placementNode) != "" && (driver == "exec" || driver == "raw_exec") && tool != "wget" {
			fmt.Fprintf(cmd.ErrOrStderr(), "[abc] warning: --node pins the job to a node; with --driver=%s the node must have %q installed. Prefer --driver=containerd (Nomad: containerd-driver) or --driver=docker for pinned OCI images.\n", driver, tool)
		}

		downloadsScript, err := buildToolScript(opts, serverURL, accessToken, workspace, uploadEndpoint, uploadToken)
		if err != nil {
			return err
		}
		return submitJobWithDriver(cmd, opts, downloadsScript, driver)
	}

	if len(opts.accessions) == 0 && opts.paramsFile == "" {
		return fmt.Errorf("must provide at least one --accession or --params-file")
	}

	params, err := loadParamsFile(opts.paramsFile)
	if err != nil {
		return fmt.Errorf("failed to load params file: %w", err)
	}

	if len(opts.accessions) > 0 {
		if params == nil {
			params = map[string]any{}
		}
		if len(opts.accessions) == 1 {
			params["accession"] = opts.accessions[0]
		} else {
			params["accession"] = opts.accessions
		}
	}

	configText, err := loadTextFile(opts.configFile)
	if err != nil {
		return fmt.Errorf("failed to load config file: %w", err)
	}

	req := &api.PipelineRunRequest{
		Pipeline:   "https://github.com/nf-core/fetchngs",
		RunName:    opts.runName,
		Revision:   opts.revision,
		Profile:    opts.profile,
		WorkDir:    opts.workDir,
		ConfigText: configText,
		Params:     params,
	}

	client := factory(serverURL, accessToken, workspace)
	resp, err := client.SubmitPipelineRun(req)
	if err != nil {
		return fmt.Errorf("data download pipeline submission failed: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Data download pipeline submitted successfully.\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  Run ID:   %s\n", resp.RunID)
	if resp.RunName != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Run Name: %s\n", resp.RunName)
	}
	if resp.WorkflowID != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Workflow: %s\n", resp.WorkflowID)
	}

	return nil
}

func loadParamsFile(paramsFile string) (map[string]any, error) {
	if paramsFile == "" {
		return nil, nil
	}

	data, err := os.ReadFile(paramsFile)
	if err != nil {
		return nil, fmt.Errorf("could not read params file %q: %w", paramsFile, err)
	}

	var params map[string]any
	if json.Valid(data) {
		if err := json.Unmarshal(data, &params); err != nil {
			return nil, fmt.Errorf("invalid JSON in params file: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, &params); err != nil {
			return nil, fmt.Errorf("invalid YAML in params file: %w", err)
		}
	}

	return params, nil
}

func loadTextFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("could not read file %q: %w", path, err)
	}
	return string(data), nil
}

func shellEscape(str string) string {
	return "'" + strings.ReplaceAll(str, "'", "'\"'\"'") + "'"
}

func buildToolCommand(opts *downloadOptions) (string, error) {
	dest := opts.destination
	if dest == "" {
		dest = "/tmp/abc-data-download"
	}
	if opts.source == "" && opts.urlFile == "" {
		return "", fmt.Errorf("either --source or --url-file is required for tool %q", opts.tool)
	}

	parallel := opts.parallel
	if parallel <= 0 {
		parallel = 4
	}

	var cmd string
	extra := strings.TrimSpace(opts.toolArgs)
	if extra != "" {
		extra = " " + extra
	}

	switch strings.ToLower(opts.tool) {
	case "aria2":
		// -c resumes partial downloads using .aria2 control files at --dir.
		// --auto-file-renaming=false prevents aria2 from appending .1/.2 suffixes
		// when a partial file already exists, which would break the resume lookup.
		if opts.urlFile != "" {
			cmd = fmt.Sprintf("aria2c -c --auto-file-renaming=false --input-file=%s --dir=%s --max-concurrent-downloads=%d --max-overall-download-limit=0%s", shellEscape(opts.urlFile), shellEscape(dest), parallel, extra)
		} else {
			cmd = fmt.Sprintf("aria2c -c --auto-file-renaming=false -x %d -s %d -d %s %s%s", parallel, parallel, shellEscape(dest), shellEscape(opts.source), extra)
		}
	case "rclone":
		if opts.urlFile != "" {
			cmd = fmt.Sprintf("rclone copy --transfers=%d --files-from=%s %s %s%s", parallel, shellEscape(opts.urlFile), shellEscape(opts.source), shellEscape(dest), extra)
		} else {
			cmd = fmt.Sprintf("rclone copy --transfers=%d %s %s%s", parallel, shellEscape(opts.source), shellEscape(dest), extra)
		}
	case "wget":
		if opts.urlFile != "" {
			cmd = fmt.Sprintf("cat %s | xargs -n1 -P %d wget -c -P %s%s", shellEscape(opts.urlFile), parallel, shellEscape(dest), extra)
		} else {
			cmd = fmt.Sprintf("wget -c -P %s %s%s", shellEscape(dest), shellEscape(opts.source), extra)
		}
	case "s5cmd":
		// s5cmd global flags (e.g. --no-sign-request) must precede the subcommand.
		// extra is placed between "s5cmd" and "--numworkers" so --tool-args are treated as global flags.
		// s5cmd uses --numworkers (not --jobs) to control parallelism.
		if opts.urlFile != "" {
			cmd = fmt.Sprintf("s5cmd%s --numworkers %d cp --from-file %s %s", extra, parallel, shellEscape(opts.urlFile), shellEscape(dest))
		} else {
			cmd = fmt.Sprintf("s5cmd%s --numworkers %d cp %s %s", extra, parallel, shellEscape(opts.source), shellEscape(dest))
		}
	default:
		return "", fmt.Errorf("unsupported tool %q", opts.tool)
	}

	return cmd, nil
}

func isClusterOrBucketTarget(dest string) bool {
	// For this MVP, treat non-filesystem path string as cluster/bucket/remote target.
	if dest == "" {
		return false
	}
	if strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "./") || strings.HasPrefix(dest, "../") {
		return false
	}
	if strings.Contains(dest, "://") {
		return false
	}
	return true
}

// storageDestInfo holds the resolved S3 backend connection details and target path
// for --destination storage.
type storageDestInfo struct {
	endpoint   string
	accessKey  string
	secretKey  string
	bucketPath string // e.g. s3://downloads/su-mbhg-hostgen/researcher/
}

// resolveStorageDestInfo reads the active context to determine where --destination storage
// should write files. It prefers rustfs over MinIO and derives the target path from the
// user's Nomad namespace and whoami identity.
//
// storagePath overrides the bucket path:
//   - empty → auto-derive s3://downloads/<namespace>/<user>/
//   - starts with "s3://" → used verbatim (trailing slash appended if absent)
//   - otherwise → appended to the auto-derived base, e.g. "run1/" → s3://downloads/<ns>/<user>/run1/
func resolveStorageDestInfo(storagePath string) (*storageDestInfo, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()

	// Prefer rustfs, fall back to minio.
	endpoint := ctx.RustfsS3APIEndpoint()
	var envMap map[string]string
	if endpoint != "" {
		envMap = ctx.AbcNodesRustfsStorageCLIEnv()
	} else {
		endpoint = ctx.MinioS3APIEndpoint()
		envMap = ctx.AbcNodesMinioStorageCLIEnv()
	}
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("no S3 endpoint configured in active context " +
			"(set admin.services.rustfs.endpoint or admin.services.minio.endpoint)")
	}

	ak := strings.TrimSpace(envMap["AWS_ACCESS_KEY_ID"])
	sk := strings.TrimSpace(envMap["AWS_SECRET_ACCESS_KEY"])

	var bucketPath string
	sp := strings.TrimSpace(storagePath)
	switch {
	case strings.HasPrefix(sp, "s3://") || strings.HasPrefix(sp, "S3://"):
		bucketPath = strings.TrimRight(sp, "/") + "/"
	case sp != "":
		ns := ctx.AbcNodesNomadNamespaceOrDefault()
		user := storageUserSlug(ctx)
		base := "s3://" + ns + "/downloads/" + user + "/"
		bucketPath = base + strings.TrimLeft(sp, "/")
		if !strings.HasSuffix(bucketPath, "/") {
			bucketPath += "/"
		}
	default:
		ns := ctx.AbcNodesNomadNamespaceOrDefault()
		user := storageUserSlug(ctx)
		bucketPath = "s3://" + ns + "/downloads/" + user + "/"
	}

	return &storageDestInfo{
		endpoint:   strings.TrimRight(endpoint, "/"),
		accessKey:  ak,
		secretKey:  sk,
		bucketPath: bucketPath,
	}, nil
}

// storageUserSlug derives a safe S3 path segment from auth.whoami or admin.whoami.
// Takes the rightmost colon-separated segment, lowercased and sanitized to [a-z0-9-].
func storageUserSlug(ctx config.Context) string {
	whoami := ""
	if ctx.Auth != nil {
		whoami = strings.TrimSpace(ctx.Auth.Whoami)
	}
	if whoami == "" {
		whoami = strings.TrimSpace(ctx.Admin.Whoami)
	}
	if whoami == "" {
		return "user"
	}
	parts := strings.Split(whoami, ":")
	seg := strings.TrimSpace(parts[len(parts)-1])
	var b strings.Builder
	for _, r := range strings.ToLower(seg) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_':
			b.WriteRune('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "user"
	}
	return result
}

// buildStorageScript generates a two-step bash script for --destination storage:
// Step 1 downloads from the source (with original tool-args, e.g. --no-sign-request for public S3).
// Step 2 pushes the local temp files to the cluster's S3 backend using s5cmd with resolved creds.
func buildStorageScript(opts *downloadOptions) (string, error) {
	info, err := resolveStorageDestInfo(opts.storagePath)
	if err != nil {
		return "", fmt.Errorf("--destination storage: %w", err)
	}

	const tmpDir = "/tmp/abc-data-dl-tmp"

	// Build the download command pointing at the local temp dir.
	localOpts := *opts
	localOpts.destination = tmpDir
	localCmd, err := buildToolCommand(&localOpts)
	if err != nil {
		return "", err
	}

	parallel := opts.parallel
	if parallel <= 0 {
		parallel = 4
	}

	var sb strings.Builder
	// Banner — surfaces the resolved plan up front so the alloc logs are
	// self-describing before any tool runs.
	sb.WriteString("echo '╭─ abc data download ─────────────────────────────────────────╮'\n")
	sb.WriteString(fmt.Sprintf("echo '│ tool      : %s'\n", opts.tool))
	sb.WriteString(fmt.Sprintf("echo '│ source    : %s'\n", opts.source+opts.urlFile))
	sb.WriteString(fmt.Sprintf("echo '│ staging   : %s'\n", tmpDir))
	sb.WriteString(fmt.Sprintf("echo '│ s3 endpoint : %s'\n", info.endpoint))
	sb.WriteString(fmt.Sprintf("echo '│ s3 destination : %s'\n", info.bucketPath))
	if info.accessKey != "" {
		sb.WriteString(fmt.Sprintf("echo '│ s3 user   : %s (key from active context)'\n", info.accessKey))
	}
	sb.WriteString(fmt.Sprintf("echo '│ workers   : %d'\n", parallel))
	sb.WriteString("echo '│ alloc     : '${NOMAD_ALLOC_ID:-?}'  task: '${NOMAD_TASK_NAME:-?}\n")
	sb.WriteString("echo '│ namespace : '${NOMAD_NAMESPACE:-?}'  node: '${NOMAD_NODE_NAME:-?}\n")
	sb.WriteString("echo '╰──────────────────────────────────────────────────────────────╯'\n")
	sb.WriteString("echo\n")

	sb.WriteString(fmt.Sprintf("mkdir -p %s\n", shellEscape(tmpDir)))
	sb.WriteString("echo '── [1/2] downloading from source ──'\n")
	sb.WriteString("_t0=$(date +%s)\n")
	sb.WriteString(localCmd + "\n")
	sb.WriteString(fmt.Sprintf("_files=$(find %s -type f | wc -l | tr -d ' ')\n", shellEscape(tmpDir)))
	sb.WriteString(fmt.Sprintf("_bytes=$(du -sb %s 2>/dev/null | awk '{print $1}')\n", shellEscape(tmpDir)))
	sb.WriteString("printf '   ✓ fetched %s file(s), %s bytes in %ds\\n' \"$_files\" \"${_bytes:-?}\" \"$(($(date +%s)-_t0))\"\n")
	sb.WriteString("echo\n")

	sb.WriteString(fmt.Sprintf("echo '── [2/2] uploading to %s ──'\n", info.bucketPath))
	if info.accessKey != "" {
		sb.WriteString(fmt.Sprintf("export AWS_ACCESS_KEY_ID=%s\n", shellEscape(info.accessKey)))
	}
	if info.secretKey != "" {
		sb.WriteString(fmt.Sprintf("export AWS_SECRET_ACCESS_KEY=%s\n", shellEscape(info.secretKey)))
	}
	// s5cmd 2.x hangs awaiting region resolution if AWS_REGION is unset on
	// non-AWS endpoints. us-east-1 is a safe default for RustFS/MinIO.
	sb.WriteString("export AWS_REGION=\"${AWS_REGION:-us-east-1}\"\n")
	sb.WriteString("_t0=$(date +%s)\n")
	// Pass --endpoint-url as a flag; AWS_ENDPOINT_URL env var is not reliably picked up
	// by s5cmd 2.x on all platforms.
	sb.WriteString(fmt.Sprintf("s5cmd --endpoint-url %s --numworkers %d cp '%s/*' %s\n",
		shellEscape(info.endpoint), parallel, tmpDir, info.bucketPath))
	sb.WriteString("printf '   ✓ uploaded in %ds\\n' \"$(($(date +%s)-_t0))\"\n")
	sb.WriteString("echo\n")
	sb.WriteString(fmt.Sprintf("echo '✓ done — files now at %s'\n", info.bucketPath))
	sb.WriteString(fmt.Sprintf("rm -rf %s\n", shellEscape(tmpDir)))
	return sb.String(), nil
}

func buildToolScript(opts *downloadOptions, serverURL, accessToken, workspace, uploadEndpoint, uploadToken string) (string, error) {
	// --destination storage: resolve cluster S3 backend and emit a two-step script.
	if opts.destination == "storage" {
		return buildStorageScript(opts)
	}

	cmdLine, err := buildToolCommand(opts)
	if err != nil {
		return "", err
	}

	dest := opts.destination
	if dest == "" {
		dest = "/tmp/abc-data-download"
	}

	// s3:// destinations are handled directly by tools like s5cmd — no local mkdir needed.
	isS3Dest := strings.HasPrefix(dest, "s3://") || strings.HasPrefix(dest, "S3://")

	var sb strings.Builder
	if !isS3Dest {
		sb.WriteString(fmt.Sprintf("mkdir -p %s\n", shellEscape(dest)))
	}

	// s5cmd on non-AWS hosts (GCP, on-prem) has no AWS region configured.
	// Without a region, it defaults to eu-west-1 and fails on us-east-1 buckets
	// (e.g. SRA) with a BucketRegionError. us-east-1 is the correct default for
	// public AWS S3 access when no endpoint override is specified.
	if strings.ToLower(opts.tool) == "s5cmd" && !isS3Dest {
		sb.WriteString("export AWS_DEFAULT_REGION=us-east-1\n")
	}

	sb.WriteString("echo '=== Downloading files ==='\n")
	sb.WriteString(cmdLine + "\n")

	// For s3:// destinations the tool writes directly to object storage — no upload step.
	if isS3Dest {
		return sb.String(), nil
	}

	if opts.destination == "" {
		return sb.String(), nil
	}

	if opts.destination == "abc-bucket" {
		if strings.TrimSpace(uploadEndpoint) == "" {
			return "", fmt.Errorf("upload endpoint is empty; set contexts.<name>.upload_endpoint, ABC_UPLOAD_ENDPOINT, or a valid API --url for derived <api>/files/")
		}
		uploadCmd := fmt.Sprintf("abc data upload --url=%s --endpoint=%s", shellEscape(serverURL), shellEscape(uploadEndpoint))
		if strings.TrimSpace(uploadToken) != "" {
			uploadCmd += fmt.Sprintf(" --upload-token=%s", shellEscape(uploadToken))
		}
		uploadCmd += fmt.Sprintf(" --access-token=%s --workspace=%s", shellEscape(accessToken), shellEscape(workspace))
		sb.WriteString("echo '=== Uploading to TUS (abc-bucket) ==='\n")
		sb.WriteString(fmt.Sprintf("find %s -type f -print0 | while IFS= read -r -d '' f; do %s \"$f\"; done\n", shellEscape(dest), uploadCmd))
		return sb.String(), nil
	}

	if isClusterOrBucketTarget(opts.destination) {
		sb.WriteString("echo '=== Uploading via rclone dynamic target ==='\n")
		sb.WriteString("cat > /tmp/rclone.conf <<'EOF'\n")
		sb.WriteString("[target]\ntype = s3\nendpoint = https://example-s3-endpoint\naccess_key_id = $RCLONE_ACCESS_KEY\nsecret_access_key = $RCLONE_SECRET_KEY\nregion = us-east-1\n\nEOF\n")
		sb.WriteString(fmt.Sprintf("rclone --config /tmp/rclone.conf copy %s target:%s --progress\n", shellEscape(dest), shellEscape(opts.destination)))
		return sb.String(), nil
	}

	return sb.String(), nil
}

// s5cmdVersion is the pinned s5cmd release fetched from the rustfs public bucket
// when the exec driver is used (no container image available on the node).
const s5cmdVersion = "v2.1.0"

// rustfsPublicBase returns the HTTP base URL for rustfs public bucket downloads.
// It uses the S3 API endpoint directly (the public bucket is accessible without
// auth on the same port after anonymous read is enabled via mc).
func rustfsPublicBase() string {
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	ep := cfg.ActiveCtx().RustfsS3APIEndpoint()
	return strings.TrimRight(ep, "/")
}

// s5cmdBootstrapSnippet returns a bash snippet that downloads the s5cmd binary
// into ${NOMAD_TASK_DIR} and prepends it to PATH. It tries the internal rustfs
// first (short connect timeout), then falls back to the official GitHub release.
// Returns an empty string if rustfsBase is empty (no rustfs configured).
func s5cmdBootstrapSnippet(rustfsBase string) string {
	if rustfsBase == "" {
		return ""
	}
	// GitHub release filenames use the bare version (2.1.0), not the v-prefixed tag (v2.1.0).
	fileVersion := strings.TrimPrefix(s5cmdVersion, "v")
	ghBase := fmt.Sprintf("https://github.com/peak/s5cmd/releases/download/%s", s5cmdVersion)
	return fmt.Sprintf(`set -x
_s5cmd_arch=$(uname -m)
case "$_s5cmd_arch" in
  aarch64|arm64) _s5cmd_arch=arm64; _s5cmd_gharch=arm64 ;;
  *) _s5cmd_arch=amd64; _s5cmd_gharch=64bit ;;
esac
_s5cmd_dest="${NOMAD_TASK_DIR}/s5cmd"
_rustfs_url=%s/abc-reserved/binary_tools/s5cmd-linux-${_s5cmd_arch}
_gh_url=%s/s5cmd_%s_Linux-${_s5cmd_gharch}.tar.gz
_fetched=false
# Try internal rustfs first (connect-timeout avoids long hangs on cloud nodes).
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --connect-timeout 5 -o "${_s5cmd_dest}" "${_rustfs_url}" 2>/dev/null && _fetched=true
  if ! $_fetched; then
    echo "Rustfs unreachable, downloading s5cmd from GitHub..."
    _tarball="${NOMAD_TASK_DIR}/s5cmd.tar.gz"
    curl -fsSL -o "${_tarball}" "${_gh_url}"
    tar -xzf "${_tarball}" -C "${NOMAD_TASK_DIR}" s5cmd
    rm -f "${_tarball}"
    _fetched=true
  fi
elif command -v wget >/dev/null 2>&1; then
  wget -T5 -qO "${_s5cmd_dest}" "${_rustfs_url}" 2>/dev/null && _fetched=true
  if ! $_fetched; then
    echo "Rustfs unreachable, downloading s5cmd from GitHub..."
    _tarball="${NOMAD_TASK_DIR}/s5cmd.tar.gz"
    wget -qO "${_tarball}" "${_gh_url}"
    tar -xzf "${_tarball}" -C "${NOMAD_TASK_DIR}" s5cmd
    rm -f "${_tarball}"
    _fetched=true
  fi
else
  echo "ERROR: neither curl nor wget found on this node" >&2
  exit 1
fi
chmod +x "${_s5cmd_dest}"
export PATH="${NOMAD_TASK_DIR}:${PATH}"
set +x
`, rustfsBase, ghBase, fileVersion)
}

// needsS5cmdBootstrap reports whether the task requires s5cmd to be staged at
// runtime (exec/raw_exec nodes don't have s5cmd pre-installed).
func needsS5cmdBootstrap(driver, tool, destination string) bool {
	if driver != "exec" && driver != "raw_exec" {
		return false
	}
	// Direct s5cmd download job.
	if strings.ToLower(tool) == "s5cmd" {
		return true
	}
	// Two-step storage upload: Step 2 uses s5cmd regardless of the download tool.
	if destination == "storage" {
		return true
	}
	return false
}

func submitJobWithDriver(cmd *cobra.Command, opts *downloadOptions, taskBody, driver string) error {
	// Default RunName to the recognised "data-download" category prefix so the
	// generated Nomad job ID becomes `<user>-data-download-<id>` rather than
	// inheriting the random temp-script basename (which produced opaque IDs
	// like `<user>-script-job-abc-data-1341971977-…`).
	runName := strings.TrimSpace(opts.runName)
	if runName == "" {
		runName = "data-download"
	}
	submitOpts := dataNomadScriptOpts{
		RunName:       runName,
		PlacementNode: opts.placementNode,
		Driver:        driver,
		Tool:          opts.tool,
	}

	tool := strings.ToLower(opts.tool)

	if driver == "exec" || driver == "raw_exec" {
		var artifacts []downloadArtifact

		// Stage the primary download tool via Nomad artifact stanza when it is
		// registered in tools.toml (preferred over runtime bash bootstrapping).
		if tool != "wget" {
			if artURL := toolNomadArtifactURL(tool); artURL != "" {
				artifacts = append(artifacts, downloadArtifact{
					URL:  artURL,
					Dest: "local/" + tool,
					Mode: "file",
				})
			}
		}

		// Stage s5cmd for the --destination storage upload step when the primary
		// tool is not s5cmd itself (s5cmd is already covered above in that case).
		if opts.destination == "storage" && tool != "s5cmd" {
			if artURL := toolNomadArtifactURL("s5cmd"); artURL != "" {
				artifacts = append(artifacts, downloadArtifact{
					URL:  artURL,
					Dest: "local/s5cmd",
					Mode: "file",
				})
			}
		}

		if len(artifacts) > 0 {
			submitOpts.Artifacts = artifacts
			// Nomad artifact `destination = "local/<name>"` lands the file at
			// ${NOMAD_TASK_DIR}/<name> because NOMAD_TASK_DIR *is* the task's
			// local/ directory. Prepend it to PATH and chmod +x so plain-name
			// invocation works for every artifact-staged tool.
			var prelude strings.Builder
			prelude.WriteString("export PATH=\"${NOMAD_TASK_DIR}:${PATH}\"\n")
			for _, art := range artifacts {
				name := strings.TrimPrefix(art.Dest, "local/")
				if name == "" {
					continue
				}
				prelude.WriteString(fmt.Sprintf("chmod +x \"${NOMAD_TASK_DIR}/%s\" 2>/dev/null || true\n", name))
			}
			taskBody = prelude.String() + taskBody
		} else if needsS5cmdBootstrap(driver, tool, opts.destination) {
			// Fall back to inline bash bootstrap when tools.toml endpoint is not configured.
			if snippet := s5cmdBootstrapSnippet(rustfsPublicBase()); snippet != "" {
				taskBody = snippet + taskBody
			}
		}
	}

	return submitDataNomadScript(cmd, submitOpts, taskBody)
}

// toolNomadArtifactURL returns the Nomad artifact source URL for toolName,
// embedding ${attr.kernel.name} and ${attr.cpu.arch} so Nomad resolves the
// correct binary for each node at scheduling time.
//
// Returns empty string when tools.toml is absent, the tool is not listed, or
// the endpoint has not yet been written back by `abc admin tools push`.
func toolNomadArtifactURL(toolName string) string {
	assetDir, err := utils.AssetDir()
	if err != nil {
		return ""
	}

	type pushSection struct {
		Bucket string `toml:"bucket"`
		Prefix string `toml:"prefix"`
	}
	type toolEntry struct {
		Disabled bool `toml:"disabled"`
	}
	type localEntry struct {
		Disabled bool `toml:"disabled"`
	}
	type rawCfg struct {
		Push  pushSection           `toml:"push"`
		Tools map[string]toolEntry  `toml:"tools"`
		Local map[string]localEntry `toml:"local"`
	}

	data, err := os.ReadFile(filepath.Join(assetDir, "tools.toml"))
	if err != nil {
		return ""
	}
	var rc rawCfg
	if err := tomlparse.Unmarshal(data, &rc); err != nil {
		return ""
	}

	// Verify the tool is known (either in [tools.*] or [local.*]) and not disabled.
	known := false
	if t, ok := rc.Tools[toolName]; ok && !t.Disabled {
		known = true
	}
	if !known {
		if l, ok := rc.Local[toolName]; ok && !l.Disabled {
			known = true
		}
	}
	if !known {
		return ""
	}

	bucket := rc.Push.Bucket
	if bucket == "" {
		bucket = "abc-reserved"
	}
	prefix := rc.Push.Prefix
	if prefix == "" {
		prefix = "binary_tools"
	}

	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	endpoint := strings.TrimRight(cfg.ActiveCtx().ToolPushEndpoint(), "/")
	if endpoint == "" {
		return ""
	}

	return fmt.Sprintf("%s/%s/%s/%s-${attr.kernel.name}-${attr.cpu.arch}",
		endpoint, bucket, prefix, toolName)
}
