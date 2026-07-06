package pipeline

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type Spec struct {
	Name string

	WorkDir string
	Params  map[string]any

	// HeadNomadAddr is the NOMAD_ADDR injected into the head task env — the
	// address nf-nomad uses to register WORKER jobs. It should be the cluster's
	// INTERNAL Nomad API (the head runs on-cluster), NOT the public ingress URL
	// the CLI itself dials: routing worker registers through the public proxy
	// (tailscaled-Serve → Traefik) masked real 403s as empty ApiExceptions and
	// is a fragile control-plane path (see brainstorms/abc-seedling-prod/
	// 2026-06-08-nf-nomad-concurrent-submit-conn-cap.md). When empty the
	// generator falls back to the CLI's nomadAddr (legacy behaviour).
	HeadNomadAddr string

	CPU             int
	MemoryMB        int
	// HeadDiskMB is the head group's ephemeral_disk size in MB. The head needs
	// real scratch when Nextflow stages foreign input files (e.g. ftp:// FASTQs
	// pulled into the workdir at workflow init) — Nomad's 300MB default is far
	// too small and the head crash-loops mid-staging. Default applied in run.go.
	HeadDiskMB      int
	NfVersion       string
	NfPluginVersion string

	Namespace   string
	Datacenters []string

	Repository  string
	Revision    string
	Profile     string
	ExtraConfig string

	// Resume appends -resume to the nextflow run command (checkpoint restart).
	Resume bool
	// SessionID is the Nextflow session UUID to resume from (only meaningful
	// when Resume is true). Distinct from RunTag below — Nextflow's session
	// UUID is data-plane (cache locality); the run tag is orchestration.
	SessionID string
	// PinnedSessionUUID, when set, is the Nextflow session UUID the head
	// pins and always resumes (`-resume <uuid>`). Set for S3-cloudcache runs
	// regardless of whether the user asked to resume: a fresh run pins a new
	// UUID, a user resume pins SessionID. Because the head entrypoint is a
	// static script, a Nomad task restart OR reschedule re-runs the SAME
	// `-resume <uuid>` — so the head transparently picks up completed tasks
	// from the cloudcache instead of redoing them. On the first run the cache
	// is empty so it behaves exactly like a fresh run (verified). Requires
	// NXF_IGNORE_RESUME_HISTORY=true (set alongside NXF_CLOUDCACHE_PATH).
	PinnedSessionUUID string
	// RunTag is a short alphanumeric prefix that the harness pre-chose for
	// single-prefix Nomad correlation. Emitted on the head task as
	// `NF_NOMAD_RUN_TAG`. Combined with PipelineSlug below.
	RunTag string
	// NextflowRunName, when set, is passed verbatim as `nextflow run -name`.
	// Used for resume lineages (`<base>_1`, `<base>_2`, …) so successive
	// resumes of the same work-dir get distinct, traceable run names. When
	// empty the generator falls back to RunTag (the fresh-run default).
	NextflowRunName string
	// PipelineSlug is the sanitized slug derived from the pipeline URL
	// (e.g. `nf-core-demo` from `nf-core/demo`). Used as the trailing
	// segment of the head Nomad job-id (`<runtag>-nf-head-<slug>`) for
	// human readability. NOT emitted to nf-nomad — children encode
	// pipeline context in the process name already.
	PipelineSlug string

	// HostVolume is the Nomad host volume name used for the shared work directory.
	// Defaults to "nextflow-work". Override with the name of any host volume
	// available on the target nodes (e.g. "scratch").
	// Set to "-" to skip the host volume block entirely (use with S3 work dirs).
	HostVolume string

	// NodeConstraint pins the head job to a specific Nomad node hostname.
	// When set, a constraint { attribute = "${attr.unique.hostname}" value = "<node>" }
	// block is added to the head group.
	NodeConstraint string

	// PinWorkers, when true AND NodeConstraint is set, also emits the per-process
	// nf-nomad constraint `process { constraints = { node { unique = [name: '<node>'] } } }`
	// — pinning EVERY child Nomad task to the same node. Use this for runs without
	// a shared filesystem when nf-rclone or similar isn't available. Default false:
	// `--node` only pins the head; workers spread freely across the cluster.
	PinWorkers bool

	// WorkerExcludeHost, when set, emits a per-process Nomad constraint
	// `${node.unique.name} != <host>` for each entry. Use it to force a true
	// head≠worker distributed test — workers cannot accidentally co-locate on
	// the head's node and bypass the no-shared-FS code path (rclone bootstrap,
	// .exitcode upload via remote, etc.). Typically set to the same host as
	// NodeConstraint for the canonical "head pinned, workers excluded" pattern.
	WorkerExcludeHost []string

	// HeadPool is the Nomad node-pool name the head job must land in.
	// On seedling: "platform". When empty, no `node_pool = …` attribute
	// is emitted on the head job, so Nomad uses its `default` pool —
	// fine for single-pool clusters; placement-failure on clusters that
	// require all jobs to declare a non-default pool.
	HeadPool string

	// WorkerPool is the Nomad node-pool name nf-nomad-spawned worker jobs
	// (per-process) should land in. On seedling: "compute". When empty,
	// no `nomad.jobs.nodePool = …` line is emitted in the generated
	// nextflow.headjob.config; workers go to Nomad's default pool. Still
	// emitted even when PinWorkers is set — see workerNodePoolLine's doc
	// comment for why the two must combine, not conflict, on any cluster
	// that uses named pools (i.e. every real seedling-prod node).
	WorkerPool string

	// PluginBundleURL, when non-empty, makes the head job pull this artifact zip
	// (typically the nf-abc-cluster-dev meta-plugin bundle) and extract it into
	// $NXF_HOME/plugins before running Nextflow. Used to ship development plugin
	// variants that are not on the public registry.
	PluginBundleURL string

	// NextflowBinURL, when non-empty, makes the head job pull a custom Nextflow
	// fork zip (typically registered as nextflow-dev-any in the tools registry)
	// and extract it into local/nextflow-dev/. The launcher script is then
	// prepended to PATH, shadowing the image's built-in nextflow binary. This
	// allows testing core patches (e.g. per-process multi-driver support) without
	// rebuilding the head Docker image.
	NextflowBinURL string

	// Plugins is the ordered list of `id "<id>@<version>"` lines emitted in the
	// generated nextflow.headjob.config plugins { ... } block. When empty, the
	// existing single-line "nf-nomad@<NfPluginVersion>" behaviour is preserved
	// for backwards compatibility.
	Plugins []PluginRef

	// ExtraBinaries is an ordered list of (name, source URL) pairs for
	// additional cluster tool binaries the head task needs at runtime —
	// e.g. `rclone` when the nf-rclone plugin is loaded. Each entry produces
	// a Nomad `artifact` stanza pulling into local/bin/<name> + chmod +x;
	// local/bin is prepended to PATH in the entrypoint.
	ExtraBinaries []ToolBinary

	// StaticEnv is merged into the task env block as literal strings (abc-nodes
	// enhanced floor: Loki, Prometheus, Grafana Alloy).
	StaticEnv map[string]string

	// Identity carries the user/workspace correlation context that's emitted
	// onto the head Job's `meta {}` block AND injected as env vars on the head
	// task. nf-nomad reads from those env vars at session-init and mirrors the
	// same keys onto every child Nomad job's meta — so a single allocation
	// inspect tells you who/where/when without external state. See
	// docs/design/workspace-model-and-job-correlation.md.
	Identity Identity

	// WaveEndpoint, when non-empty, emits a wave { enabled = true; endpoint = "..." }
	// block in the Groovy nextflow config. Set to abc-wave URL for local augmentation
	// or to wave.seqera.io for the public service. Empty = no wave block emitted.
	WaveEndpoint string

	// FusionEnabled, when true AND WaveEndpoint is set, also emits a
	// fusion { enabled = true } block. Fusion requires Wave and an S3 work dir,
	// and always routes to wave.seqera.io (Wave Lite does not support Fusion).
	FusionEnabled bool

	// WaveTokenSecret is the Nomad Variable path+key for the Seqera access token
	// injected as TOWER_ACCESS_TOKEN when FusionEnabled = true.
	// Format: "nomad/path:key" (e.g. "nomad/jobs:wave_token").
	// Defaults to "nomad/jobs:wave_token" when empty and FusionEnabled is set.
	WaveTokenSecret string

	// S5cmdSkipTLS, when true, emits `useTLS = false` in the generated
	// nomad.s5cmd.s3 config block. Set this when the cluster's S3 endpoint
	// uses a private CA certificate that worker container images don't trust.
	// Without it, s5cmd calls inside the worker bootstrap script fail with
	// "x509: certificate signed by unknown authority".
	//
	// Automatically set by the CLI when the active context's MinIO endpoint
	// starts with "https://" — abc-seedling always uses HTTPS with a private
	// CA, so every container that doesn't mount the CA bundle needs this.
	S5cmdSkipTLS bool

	// ContainerRuntime selects the Nextflow container engine. Valid values:
	//   ""  or "docker"     → docker  { enabled = true }  (default)
	//   "singularity"       → singularity { enabled = true; ociAutoPull = true* }
	//   "apptainer"         → apptainer  { enabled = true; ociAutoPull = true* }
	// *ociAutoPull is only emitted when WaveEndpoint is set so Singularity/Apptainer
	// pulls the augmented OCI image from the Wave proxy and converts it to SIF locally,
	// instead of relying on Wave to build the SIF (which Wave Lite cannot do).
	ContainerRuntime string

	// SkipNomadVarCreds, when true, omits the `secrets/aws.env` Nomad
	// template that reads AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY from
	// `nomad/jobs/secrets/*` Nomad Variables. Set this when the credentials
	// are already injected via StaticEnv (e.g. from admin.services.minio.*
	// in the active context). Without this flag, the template blocks task
	// startup on clusters where the Nomad Variable doesn't exist.
	SkipNomadVarCreds bool

	// ExtraPassthroughEnvKeys lists additional env var names (already set on
	// the head task via StaticEnv) that nf-nomad should mirror onto every
	// child Nomad job via identityEnvPassthrough. Use this for non-secret
	// runtime config (e.g. NF_MINIO_ENDPOINT) — values flow to both task
	// env AND job.meta on the child job (visible via `nomad job inspect`).
	ExtraPassthroughEnvKeys []string

	// SecretPassthroughEnvKeys lists env var names that nf-nomad should
	// mirror onto every child Nomad job's TASK ENV ONLY — never into
	// job.meta. Use this for secrets (AWS_ACCESS_KEY_ID,
	// AWS_SECRET_ACCESS_KEY, registry creds) that the worker needs at
	// runtime but must not leak through `nomad job inspect`.
	//
	// Requires nf-nomad ≥ the secretEnvPassthrough patch (2026-05-25).
	// On older nf-nomad, this config key is ignored (silently) — the
	// abc CLI keeps these out of identityEnvPassthrough so secrets never
	// reach meta even when the worker can't read them. Operators on
	// pre-patch nf-nomad must instead use the nomadVar-based credential
	// path (SkipNomadVarCreds = false).
	SecretPassthroughEnvKeys []string
}

// PluginRef is one entry in a Nextflow plugins { ... } block.
type PluginRef struct {
	ID      string
	Version string
}

// ToolBinary is one cluster tool binary pulled into the head container as a
// Nomad artifact. SourceURL must already include any per-arch interpolation
// (e.g. `${attr.kernel.name}-${attr.cpu.arch}`); resolved by the caller via
// `tools.ArtifactURL(name, "")`.
type ToolBinary struct {
	Name      string
	SourceURL string
}

// Identity is the user / workspace / pipeline correlation context emitted
// onto the head Job's `meta {}` block AND injected as ABC_*/NF_* env vars on
// the head task. nf-nomad propagates the same keys onto every child Nomad
// job's meta. Each field is optional — empty fields are omitted from the
// emitted meta/env, so partial identity (e.g. workspace yet to be wired)
// degrades cleanly. See
// `abc-cluster-cli/docs/design/workspace-model-and-job-correlation.md`.
type Identity struct {
	// User identity (from auth.<ctx>.admin.{whoami,uuid})
	UserWhoami string
	UserUUID   string

	// Workspace = Nomad namespace = RustFS bucket; opaque flat slug
	Workspace     string // e.g. "su-mbhg-bioinformatics"
	WorkspaceType string // "personal" | "shared" (optional)
	Tenant        string // root of workspace parent chain; defaults to Workspace

	// Pipeline provenance
	PipelineURL      string // e.g. "https://github.com/nextflow-io/rnaseq-nf"
	PipelineRevision string // resolved git rev / tag

	// Run-level
	RunName     string // user-supplied via --name (or auto)
	SubmittedAt string // ISO-8601 UTC, set by abc-cluster-cli at submit time
	CLIVersion  string // semver of the abc CLI binary that submitted

	// Identity classification (mirrors utils.UserIdentity — keep in sync).
	// UserKind:  "named" | "slot"        — who they are (from token, INDEPENDENT of where they ran)
	// RunOrigin: "cluster" | "external"  — where the runner ran (orthogonal to UserKind)
	// A named user running nextflow from a laptop is "named" + "external",
	// not "external" + "external" — identity follows the token, not the
	// means of submission. See cmd/utils/identity.go for full semantics.
	UserKind  string
	RunOrigin string
}

// Empty reports whether no identity fields are populated. Used to skip the
// meta + env emission entirely when running in legacy / no-identity mode.
func (i Identity) Empty() bool {
	return i.UserWhoami == "" && i.UserUUID == "" && i.Workspace == "" &&
		i.PipelineURL == "" && i.RunName == "" && i.SubmittedAt == ""
}

// MetaMap returns the identity as the meta-key map emitted on Job.Meta.
// Keys use underscores rather than dots — HCL2 disallows dotted bare
// attribute names in `meta { ... }` blocks. Downstream readers (jurist /
// xtdb / `nomad job inspect`) treat these as flat strings; the
// `abc_user_*` / `nf_pipeline_*` prefixes carry the same hierarchy that
// the dotted form did in the design doc.
func (i Identity) MetaMap() map[string]string {
	out := make(map[string]string, 12)
	if i.UserWhoami != "" {
		out["abc_user_whoami"] = i.UserWhoami
	}
	if i.UserUUID != "" {
		out["abc_user_id"] = i.UserUUID
	}
	if i.Workspace != "" {
		out["abc_workspace"] = i.Workspace
	}
	if i.WorkspaceType != "" {
		out["abc_workspace_type"] = i.WorkspaceType
	}
	if i.Tenant != "" {
		out["abc_tenant"] = i.Tenant
	} else if i.Workspace != "" {
		out["abc_tenant"] = i.Workspace
	}
	if i.PipelineURL != "" {
		out["nf_pipeline_url"] = i.PipelineURL
	}
	if i.PipelineRevision != "" {
		out["nf_pipeline_revision"] = i.PipelineRevision
	}
	if i.RunName != "" {
		out["abc_run_name"] = i.RunName
	}
	if i.SubmittedAt != "" {
		out["abc_submitted_at"] = i.SubmittedAt
	}
	if i.CLIVersion != "" {
		out["abc_cli_version"] = i.CLIVersion
	}
	if i.UserKind != "" {
		out["abc_user_kind"] = i.UserKind
	}
	if i.RunOrigin != "" {
		out["abc_run_origin"] = i.RunOrigin
	}
	return out
}

// identityPassthroughLine emits the nomad.jobs.identityEnvPassthrough config
// line (or empty when no identity fields are set AND no extra keys are given,
// so legacy runs stay byte-identical). Sorted for determinism.
func identityPassthroughLine(id Identity, extra ...string) string {
	seen := map[string]struct{}{}
	names := id.EnvVarNames()
	for _, n := range names {
		seen[n] = struct{}{}
	}
	for _, n := range extra {
		if _, ok := seen[n]; !ok {
			names = append(names, n)
			seen[n] = struct{}{}
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = `"` + n + `"`
	}
	return "identityEnvPassthrough  = [" + strings.Join(quoted, ", ") + "]"
}

// workerNodePoolLine emits the `nodePool = "<pool>"` line inside the
// generated nomad.jobs { ... } config block, which routes every
// nf-nomad-spawned worker job to the named Nomad node-pool.
//
// Emitted whenever workerPool is non-empty, REGARDLESS of pinWorkers.
// Previously this was skipped whenever `--pin-workers` was set, on the
// theory that the per-process node-name constraint alone was sufficient
// to place workers. Live testing (see
// brainstorms/abc-data-node/2026-07-04-aither-abc-tools-rw-worker-mount-report.md,
// "2026-07-05 (later)", in abc-universe) showed nf-nomad's per-task job
// submission (NomadService.submitTask) sets NodePool from
// `this.config.jobOpts().nodePool` independently of the per-process
// `constraints` directive — omitting nodePool here left every worker job
// on Nomad's "default" pool, which on seedling-prod (and any cluster that
// uses named pools) has zero registered nodes: a guaranteed, silent
// "No nodes were eligible for evaluation" scheduling failure.
//
// Emitting nodePool alongside the per-process node-name constraint is
// safe: Nomad first narrows placement to the named pool, then the
// constraint narrows further to the specific node. As long as the pinned
// --node host is actually a member of workerPool, both agree. If it is
// NOT a member, the run now fails fast with a clear placement error
// instead of the previous silent, always-broken "default pool" failure.
// Still skipped when workerPool is empty (operator/cluster doesn't use
// pools — Nomad's default is fine).
func workerNodePoolLine(workerPool string) string {
	if workerPool == "" {
		return ""
	}
	return fmt.Sprintf(`nodePool                 = %q`, workerPool)
}

// secretPassthroughLine emits the nomad.jobs.secretEnvPassthrough config
// line. Distinct from identityEnvPassthrough — nf-nomad routes these to
// child task env ONLY, never into job.meta. Use for AWS keys, registry
// creds, etc. Empty list emits nothing (legacy byte-identical).
func secretPassthroughLine(names []string) string {
	if len(names) == 0 {
		return ""
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	quoted := make([]string, len(sorted))
	for i, n := range sorted {
		quoted[i] = `"` + n + `"`
	}
	return "secretEnvPassthrough    = [" + strings.Join(quoted, ", ") + "]"
}

// hasPlugin returns true when any PluginRef in the list has the given ID.
func hasPlugin(plugins []PluginRef, id string) bool {
	for _, p := range plugins {
		if p.ID == id {
			return true
		}
	}
	return false
}

// usesAbcTools reports whether any loaded plugin sources its CLI tool from the
// abc-tools host volume (ADR-0061). Such runs MOUNT abc-tools at /nxf-work and put
// /nxf-work/bin on PATH — the binaries are never downloaded as Nomad artifacts.
func usesAbcTools(spec Spec) bool {
	return hasPlugin(spec.Plugins, "nf-nomad-s5cmd") || hasPlugin(spec.Plugins, "nf-rclone")
}

// s5cmdBucketAndPrefix splits an S3 URI (e.g. "s3://bucket/prefix/path")
// into the bucket root ("s3://bucket") and the first path segment ("prefix").
// Used to populate s5cmd.workDir.bucket and s5cmd.workDir.prefix.
func s5cmdBucketAndPrefix(workDir string) (bucket, prefix string) {
	// Strip s3:// or s3a:// scheme
	rest := workDir
	if strings.HasPrefix(rest, "s3a://") {
		rest = rest[6:]
	} else if strings.HasPrefix(rest, "s3://") {
		rest = rest[5:]
	}
	// rest = "bucket/prefix/..."
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return "s3://" + rest, ""
	}
	bucketName := rest[:slash]
	remainder := strings.TrimPrefix(rest[slash:], "/")
	// Take only the first path segment as the prefix
	if next := strings.Index(remainder, "/"); next >= 0 {
		prefix = remainder[:next]
	} else {
		prefix = remainder
	}
	return "s3://" + bucketName, prefix
}

// buildS5cmdBlock emits the nomad.s5cmd { ... } config block for nf-nomad-s5cmd.
// Credentials and endpoint are read from the head task's environment (already
// injected via StaticEnv) so workers inherit the same values via the bootstrap
// script's env exports.
//
// skipTLS, when true, adds `useTLS = false` — required when the cluster's S3
// endpoint uses a private CA that worker images don't trust (abc-seedling norm).
func buildS5cmdBlock(workDir string, skipTLS bool) string {
	// workDir staging applies only to an S3 work dir.
	workDirBlock := ""
	if isS3URI(workDir) {
		bucket, prefix := s5cmdBucketAndPrefix(workDir)
		prefixLine := ""
		if prefix != "" {
			prefixLine = fmt.Sprintf("\n      prefix  = %q", prefix)
		}
		workDirBlock = fmt.Sprintf(`    workDir {
      enabled = true
      bucket  = %q%s
    }
`, bucket, prefixLine)
	}
	tlsLine := ""
	if skipTLS {
		tlsLine = "\n      useTLS          = false   // private CA — disables cert verification"
	}
	// binary is always the abc-tools mount (ADR-0061): jobs use the mounted s5cmd,
	// never a downloaded one. The mount is emitted in buildNextflowConfig's volumes.
	return fmt.Sprintf(`  s5cmd {
    binary = '/nxf-work/bin/s5cmd'
%s    s3 {
      endpoint        = System.getenv("NF_MINIO_ENDPOINT") ?: ""
      accessKeyId     = System.getenv("AWS_ACCESS_KEY_ID") ?: ""
      secretAccessKey = System.getenv("AWS_SECRET_ACCESS_KEY") ?: ""
      usePathStyle    = true%s
    }
  }
`, workDirBlock, tlsLine)
}

// EnvVarNames returns the head-task env var names whose values nf-nomad
// should mirror onto each child Job.Meta + child task env. Emitted into
// the generated nextflow.headjob.config under
// `nomad.jobs.identityEnvPassthrough` — keeps nf-nomad ignorant of the
// abc/nxf naming scheme (it only sees the list of names this side
// supplies).
func (i Identity) EnvVarNames() []string {
	names := make([]string, 0, len(i.EnvMap()))
	for k := range i.EnvMap() {
		names = append(names, k)
	}
	return names
}

// EnvMap returns the same identity as ABC_*/NF_* env-var pairs to set on the
// head task. nf-nomad reads these via the identityEnvPassthrough list (see
// EnvVarNames) and mirrors them onto child jobs.
func (i Identity) EnvMap() map[string]string {
	out := make(map[string]string, 12)
	if i.UserWhoami != "" {
		out["ABC_USER"] = i.UserWhoami
	}
	if i.UserUUID != "" {
		out["ABC_USER_ID"] = i.UserUUID
	}
	if i.Workspace != "" {
		out["ABC_WORKSPACE"] = i.Workspace
	}
	if i.WorkspaceType != "" {
		out["ABC_WORKSPACE_TYPE"] = i.WorkspaceType
	}
	if i.Tenant != "" {
		out["ABC_TENANT"] = i.Tenant
	} else if i.Workspace != "" {
		out["ABC_TENANT"] = i.Workspace
	}
	if i.PipelineURL != "" {
		out["NXF_PIPELINE_URL"] = i.PipelineURL
	}
	if i.PipelineRevision != "" {
		out["NXF_PIPELINE_REVISION"] = i.PipelineRevision
	}
	if i.RunName != "" {
		out["ABC_RUN_NAME"] = i.RunName
	}
	if i.SubmittedAt != "" {
		out["ABC_SUBMITTED_AT"] = i.SubmittedAt
	}
	if i.CLIVersion != "" {
		out["ABC_CLI_VERSION"] = i.CLIVersion
	}
	return out
}

// generateHeadJobHCL produces a Nomad HCL job spec for a Nextflow head job
// from the given PipelineSpec and runtime credentials. runUUID must be a fresh
// unique identifier on every submission (prevents Nomad duplicate-job skip).
func Generate(spec Spec, nomadAddr, nomadToken, runUUID string) string {
	f := hclwrite.NewEmptyFile()
	root := f.Body()

	jobName := "nextflow-head"
	if spec.Name != "" {
		jobName = spec.Name
	}

	dcs := make([]cty.Value, len(spec.Datacenters))
	for i, dc := range spec.Datacenters {
		dcs[i] = cty.StringVal(dc)
	}

	jobBlock := root.AppendNewBlock("job", []string{jobName})
	jobBody := jobBlock.Body()
	jobBody.SetAttributeValue("datacenters", cty.ListVal(dcs))
	jobBody.SetAttributeValue("type", cty.StringVal("batch"))
	if spec.Namespace != "" && spec.Namespace != "default" {
		jobBody.SetAttributeValue("namespace", cty.StringVal(spec.Namespace))
	}
	// node_pool — Nomad's pool of clients eligible to run this job.
	// On clusters with all clients assigned to non-default pools (seedling-
	// prod uses platform + compute), omitting this leaves the job in the
	// "default" pool with zero eligible nodes → placement failure. The
	// CLI fills this from active context (`admin.services.nomad.head_pool`)
	// or its build-time default ("platform" on seedling).
	if spec.HeadPool != "" {
		jobBody.SetAttributeValue("node_pool", cty.StringVal(spec.HeadPool))
	}

	// run_uuid forces a new allocation on each submission.
	metaBody := jobBody.AppendNewBlock("meta", nil).Body()
	metaBody.SetAttributeValue("run_uuid", cty.StringVal(runUUID))
	if len(spec.StaticEnv) > 0 {
		metaBody.SetAttributeValue("abc_monitoring_floor", cty.StringVal("enhanced"))
	}
	// Identity correlation keys (user/workspace/pipeline). Mirrored as env
	// vars on the head task below; nf-nomad picks them up at session-init
	// and propagates onto every child Nomad job's meta.
	for _, k := range utils.SortedKeys(spec.Identity.MetaMap()) {
		metaBody.SetAttributeValue(k, cty.StringVal(spec.Identity.MetaMap()[k]))
	}

	groupBody := jobBody.AppendNewBlock("group", []string{"head"}).Body()

	// Ephemeral disk for the head group. Nomad defaults to 300MB, which is far
	// too small once Nextflow stages foreign input files (ftp:// / https:// /
	// s3:// inputs copied into the workdir at workflow init). Set a real size so
	// the head doesn't crash-loop mid-staging. Omitted when zero (legacy default).
	if spec.HeadDiskMB > 0 {
		edBody := groupBody.AppendNewBlock("ephemeral_disk", nil).Body()
		edBody.SetAttributeValue("size", cty.NumberIntVal(int64(spec.HeadDiskMB)))
	}

	// Node hostname constraint — pins the head job to a specific Nomad client.
	if spec.NodeConstraint != "" {
		cBody := groupBody.AppendNewBlock("constraint", nil).Body()
		cBody.SetAttributeValue("attribute", cty.StringVal("${attr.unique.hostname}"))
		cBody.SetAttributeValue("value", cty.StringVal(spec.NodeConstraint))
	}

	// Host volume for the shared work directory (optional; skip when using S3 work dir).
	hostVol := spec.HostVolume
	if hostVol == "" {
		hostVol = "nf-work" // default: the host volume registered on every node
		// (aither + compute). Was "nextflow-work", which no node advertises since the
		// nf-work/abc-tools host-volume standardisation — caused non-S3 heads to hang
		// pending with "missing compatible host volumes".
	}
	useHostVol := spec.HostVolume != "-" // "-" explicitly disables the host volume
	if useHostVol {
		volBody := groupBody.AppendNewBlock("volume", []string{hostVol}).Body()
		volBody.SetAttributeValue("type", cty.StringVal("host"))
		volBody.SetAttributeValue("source", cty.StringVal(hostVol))
	}

	// S3 work dir + nf-nomad-s5cmd: the HEAD task's own plugin steps (input-sweep,
	// publish) shell out to /nxf-work/bin/s5cmd just like workers do, so the head
	// must ALSO mount the abc-tools host volume at /nxf-work — not only the workers
	// (ADR-0061). This is independent of useHostVol: an S3 run sets HostVolume="-",
	// which leaves the head with no volume and its s5cmd calls failing rc=127.
	headAbcTools := usesAbcTools(spec)
	if headAbcTools && hostVol != "abc-tools" {
		atBody := groupBody.AppendNewBlock("volume", []string{"abc-tools"}).Body()
		atBody.SetAttributeValue("type", cty.StringVal("host"))
		atBody.SetAttributeValue("source", cty.StringVal("abc-tools"))
		// read-only: the head only READS s5cmd from abc-tools, and the platform node
		// (aither) registers abc-tools read-only — a RW request fails placement with
		// "missing compatible host volumes". RO matches and is correct.
		atBody.SetAttributeValue("read_only", cty.BoolVal(true))
	}

	taskBody := groupBody.AppendNewBlock("task", []string{"nextflow"}).Body()
	taskBody.SetAttributeValue("driver", cty.StringVal("docker"))

	// Resources
	resBody := taskBody.AppendNewBlock("resources", nil).Body()
	resBody.SetAttributeValue("cpu", cty.NumberIntVal(int64(spec.CPU)))
	resBody.SetAttributeValue("memory", cty.NumberIntVal(int64(spec.MemoryMB)))

	// Volume mount (only when a host volume is in use).
	if useHostVol && spec.WorkDir != "" && !isS3URI(spec.WorkDir) {
		mountBody := taskBody.AppendNewBlock("volume_mount", nil).Body()
		mountBody.SetAttributeValue("volume", cty.StringVal(hostVol))
		mountBody.SetAttributeValue("destination", cty.StringVal(spec.WorkDir))
		mountBody.SetAttributeValue("read_only", cty.BoolVal(false))
	}
	// Mount abc-tools at /nxf-work on the head whenever a tools plugin is loaded so
	// the head's own s5cmd input-sweep/publish resolves /nxf-work/bin/s5cmd (mirrors
	// the worker volume in buildNextflowConfig). Jobs mount tools, never download them.
	if headAbcTools {
		atMount := taskBody.AppendNewBlock("volume_mount", nil).Body()
		atMount.SetAttributeValue("volume", cty.StringVal("abc-tools"))
		atMount.SetAttributeValue("destination", cty.StringVal("/nxf-work"))
		atMount.SetAttributeValue("read_only", cty.BoolVal(true))
	}

	// Plugin bundle artifact — pulled and unpacked into local/plugins-bundle/.
	// The `?archive=zip` query hint forces go-getter (Nomad's artifact engine)
	// to treat the response as a zip even when the URL lacks a .zip extension
	// (e.g. our `<name>-any` S3 keys). Avoids needing `unzip` in the head image.
	if spec.PluginBundleURL != "" {
		src := spec.PluginBundleURL
		if !strings.Contains(src, "?archive=") {
			joiner := "?"
			if strings.Contains(src, "?") {
				joiner = "&"
			}
			src = src + joiner + "archive=zip"
		}
		artBody := taskBody.AppendNewBlock("artifact", nil).Body()
		artBody.SetAttributeValue("source", cty.StringVal(src))
		artBody.SetAttributeValue("destination", cty.StringVal("local/plugins-bundle"))
		artBody.SetAttributeValue("mode", cty.StringVal("any"))
	}

	// Custom Nextflow fork artifact — pulled and unpacked into local/nextflow-dev/.
	// Same archive-hint pattern as the plugin bundle. The entrypoint prepends
	// local/nextflow-dev to PATH so the fork shadows the image's built-in binary.
	if spec.NextflowBinURL != "" {
		src := spec.NextflowBinURL
		if !strings.Contains(src, "?archive=") {
			joiner := "?"
			if strings.Contains(src, "?") {
				joiner = "&"
			}
			src = src + joiner + "archive=zip"
		}
		nxfArt := taskBody.AppendNewBlock("artifact", nil).Body()
		nxfArt.SetAttributeValue("source", cty.StringVal(src))
		nxfArt.SetAttributeValue("destination", cty.StringVal("local/nextflow-dev"))
		nxfArt.SetAttributeValue("mode", cty.StringVal("any"))
	}

	// Extra cluster tool binaries (e.g. rclone, when nf-rclone is in the bundle).
	// Each one pulled as a single file into local/bin/<name>; PATH is updated
	// in the entrypoint so the head Nextflow process and any spawned
	// subprocesses can invoke them.
	for _, tb := range spec.ExtraBinaries {
		if tb.Name == "" || tb.SourceURL == "" {
			continue
		}
		// Each binary gets its own parent dir (local/bin-<name>/<name>) instead
		// of a shared local/bin/. Multiple file-mode artifacts targeting the
		// same parent directory triggered a Nomad/go-getter race in 1.11.x on
		// some agent configurations: the second artifact's staging step
		// readdirent's a path it expects to be a directory but the first
		// artifact's file handle has already populated it as a regular file,
		// surfacing as "readdirent .../artifact: not a directory". Per-binary
		// parent dirs sidestep the race entirely.
		binArt := taskBody.AppendNewBlock("artifact", nil).Body()
		binArt.SetAttributeValue("source", cty.StringVal(tb.SourceURL))
		binArt.SetAttributeValue("destination", cty.StringVal("local/bin-"+tb.Name+"/"+tb.Name))
		binArt.SetAttributeValue("mode", cty.StringVal("file"))
	}

	// Template: nextflow config
	nfCfgTmpl := taskBody.AppendNewBlock("template", nil).Body()
	nfCfgTmpl.SetAttributeValue("destination", cty.StringVal("local/nextflow.headjob.config"))
	nfCfgTmpl.SetAttributeValue("data", cty.StringVal(buildNextflowConfig(spec)))

	// Template: AWS credentials from Nomad Variables.
	//
	// Single source of truth shared with the per-process secrets directive that
	// nf-nomad uses for child tasks (see `nomad.jobs.secrets.path` in extra
	// configs). Both head and workers read AWS_ACCESS_KEY_ID /
	// AWS_SECRET_ACCESS_KEY from `nomad/jobs/secrets/<NAME>` — the operator
	// seeds these once, no per-job-name var seeding required.
	//
	// Skipped when SkipNomadVarCreds is set (credentials already injected via
	// StaticEnv). Without this guard the template blocks task startup on
	// clusters where the Nomad Variable doesn't exist or isn't accessible to
	// the task's workload identity.
	if !spec.SkipNomadVarCreds {
		awsTmpl := taskBody.AppendNewBlock("template", nil).Body()
		awsTmpl.SetAttributeValue("destination", cty.StringVal("secrets/aws.env"))
		awsTmpl.SetAttributeValue("env", cty.BoolVal(true))
		awsTmpl.SetAttributeValue("data", cty.StringVal(
			"{{- with nomadVar \"nomad/jobs/secrets/AWS_ACCESS_KEY_ID\" -}}\n"+
				"AWS_ACCESS_KEY_ID={{ .AWS_ACCESS_KEY_ID }}\n"+
				"{{- end }}\n"+
				"{{- with nomadVar \"nomad/jobs/secrets/AWS_SECRET_ACCESS_KEY\" -}}\n"+
				"AWS_SECRET_ACCESS_KEY={{ .AWS_SECRET_ACCESS_KEY }}\n"+
				"{{- end }}\n",
		))
	}

	// Template: Seqera / Tower access token — injected as TOWER_ACCESS_TOKEN and
	// SEQERA_ACCESS_TOKEN when Fusion is enabled. Fusion v2 requires these for Wave
	// to inject the Fusion agent layer. The inner {{ if }} guard prevents an empty
	// variable value from being exported, which would cause Wave to return 401.
	if spec.FusionEnabled {
		tokenPath, tokenKey := parseWaveTokenSecret(spec.WaveTokenSecret)
		tokenData := fmt.Sprintf(
			"{{- with nomadVar %q -}}\n"+
				"{{- if .%s }}\n"+
				"TOWER_ACCESS_TOKEN={{ .%s }}\n"+
				"SEQERA_ACCESS_TOKEN={{ .%s }}\n"+
				"{{- end }}\n"+
				"{{- end }}\n",
			tokenPath, tokenKey, tokenKey, tokenKey,
		)
		waveTmpl := taskBody.AppendNewBlock("template", nil).Body()
		waveTmpl.SetAttributeValue("destination", cty.StringVal("secrets/wave.env"))
		waveTmpl.SetAttributeValue("env", cty.BoolVal(true))
		waveTmpl.SetAttributeValue("data", cty.StringVal(tokenData))
	}

	// Template: params.json (only when pipeline params are provided)
	if len(spec.Params) > 0 {
		paramsJSON, _ := json.Marshal(spec.Params)
		paramsTmpl := taskBody.AppendNewBlock("template", nil).Body()
		paramsTmpl.SetAttributeValue("destination", cty.StringVal("local/params.json"))
		paramsTmpl.SetAttributeValue("data", cty.StringVal(string(paramsJSON)))
	}

	// Template: entrypoint script
	entrypointTmpl := taskBody.AppendNewBlock("template", nil).Body()
	entrypointTmpl.SetAttributeValue("destination", cty.StringVal("local/entrypoint.sh"))
	entrypointTmpl.SetAttributeValue("perms", cty.StringVal("755"))
	entrypointTmpl.SetAttributeValue("data", cty.StringVal(buildEntrypoint(spec)))

	// Docker config
	cfgBody := taskBody.AppendNewBlock("config", nil).Body()
	cfgBody.SetAttributeValue("image", cty.StringVal("nextflow/nextflow:"+spec.NfVersion))
	cfgBody.SetAttributeValue("work_dir", cty.StringVal("/local"))
	cfgBody.SetAttributeValue("command", cty.StringVal("bash"))
	cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{cty.StringVal("/local/entrypoint.sh")}))

	// Environment
	envBody := taskBody.AppendNewBlock("env", nil).Body()
	// The head submits WORKER jobs via this address. Prefer the internal Nomad
	// API (spec.HeadNomadAddr, typically the node-local agent via
	// ${attr.unique.network.ip-address}) so registers never traverse the public
	// ingress; fall back to the CLI's dial address when unset.
	headAddr := nomadAddr
	if spec.HeadNomadAddr != "" {
		headAddr = spec.HeadNomadAddr
	}
	envBody.SetAttributeValue("NOMAD_ADDR", cty.StringVal(headAddr))
	envBody.SetAttributeValue("NOMAD_TOKEN", cty.StringVal(nomadToken))
	// Run tag for single-prefix correlation. nf-nomad's NomadHelper composes
	// `<runtag>-<8task>-` as the prefix on every child Nomad job-id;
	// abc-cluster-cli builds the head as `<runtag>-nf-head-<pipeline-slug>`,
	// so a single `nomad job status -prefix <runtag>-` lists head +
	// workers. The pipeline slug is not propagated to children — the
	// process name already encodes pipeline context (e.g.
	// `NFCORE_DEMO_DEMO_FASTQC`).
	if spec.RunTag != "" {
		envBody.SetAttributeValue("NF_NOMAD_RUN_TAG", cty.StringVal(spec.RunTag))
	}
	for _, k := range utils.SortedKeys(spec.StaticEnv) {
		envBody.SetAttributeValue(k, cty.StringVal(spec.StaticEnv[k]))
	}
	// Identity envs (ABC_*, NXF_PIPELINE_*). Read by nf-nomad at session
	// init; mirrored onto child Nomad jobs' meta. Empty fields are skipped.
	for _, k := range utils.SortedKeys(spec.Identity.EnvMap()) {
		envBody.SetAttributeValue(k, cty.StringVal(spec.Identity.EnvMap()[k]))
	}

	return utils.PrettyPrintHCL(string(f.Bytes()))
}

// isS3URI returns true if the path starts with s3:// or s3a://.
func isS3URI(path string) bool {
	return strings.HasPrefix(path, "s3://") || strings.HasPrefix(path, "s3a://")
}

// parseWaveTokenSecret splits a "nomad/path:key" secret reference into its
// Nomad Variable path and key. Falls back to "nomad/jobs" / "wave_token" when
// the input is empty or malformed — the same default used by wave-exec jobs.
func parseWaveTokenSecret(s string) (path, key string) {
	if s == "" {
		return "nomad/jobs", "wave_token"
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, "wave_token"
}

// buildNextflowConfig generates the Groovy nextflow config embedded in the
// head job. It closely mirrors nextflow.headjob.config from the infra scripts.
func buildNextflowConfig(spec Spec) string {
	var sb strings.Builder

	// nf-nomad volumes block. When work dir is S3 and nf-nomad-s5cmd is in the
	// plugin list, mount a tools host volume as /nxf-work so workers can
	// find the s5cmd binary at /nxf-work/bin/s5cmd (ADR-0061; was nf-work, see below).
	// Otherwise omit the volume when S3 is the work dir (no shared local disk needed).
	hostVol := spec.HostVolume
	if hostVol == "" || hostVol == "-" {
		hostVol = "nf-work" // registered on every node (was "nextflow-work")
	}
	// Use Groovy map literal syntax ([key: val, ...]) rather than the DSL closure form
	// ({ type "host" name "nf-work" ... }) to avoid a Nextflow ≥26.04.2 config-parser
	// regression: the parser now chokes on `type` as a bare identifier inside a list
	// closure (it treats it as a reserved keyword). Map syntax is unambiguous to any
	// Groovy/Nextflow parser version.
	// Worker host volumes, composed from up to two independent mounts:
	//   (1) the shared work-dir host volume — only for a non-S3 work dir (an S3 work
	//       dir needs no shared local disk; HostVolume="-" also disables it);
	//   (2) the tools volume at /nxf-work — whenever a tools plugin
	//       (nf-nomad-s5cmd / nf-rclone) is loaded, so the worker bootstrap resolves
	//       /nxf-work/bin/s5cmd from the MOUNT (ADR-0061), never an artifact download.
	//
	// ADR-0061 readOnly status (verified against nf-nomad's JobBuilder.groovy /
	// JobVolume.groovy): nf-nomad auto-promotes whichever volume is FIRST in the
	// list to `workDir` when none is explicitly marked, and JobVolume.validate()
	// rejects `workDir && readOnly`. Ordering here is load-bearing:
	//   - Non-S3 work dir: the shared host volume is appended FIRST, so IT becomes
	//     the implicit workDir; `abc-tools` is the SECOND entry and is therefore
	//     safe to mount read-only (ADR-0061 option ii) — no plugin/validate conflict.
	//   - S3 work dir: the tools volume is the ONLY entry, so it is itself
	//     auto-promoted to workDir and MUST stay read-write. `abc-tools` is
	//     registered read-only on the `aither` platform node (a prior RW request
	//     failed placement there — see the 2026-06-26 tool-distribution
	//     brainstorm), so an S3-workdir worker landing on aither would fail to
	//     register entirely if it tried to mount abc-tools here. A separate,
	//     still-open issue (design/decided/nf-s5cmd-distributed-workdir.md
	//     "Known gaps", 2026-06-24 live EVEREST failure) also saw workers get an
	//     EMPTY /nxf-work from abc-tools even on nodes where it IS read-write,
	//     suspected to be a workload-identity token grant issue independent of
	//     the RO/RW registration. That doc's own recommended interim unblock —
	//     never previously implemented — is to use `nf-work` (registered
	//     read-write on every node, aither included, and the proven carrier
	//     nf-s5cmd's v1 design validated end-to-end on `nextflow-io/rnaseq-nf`
	//     before ADR-0061 introduced abc-tools) as the S3-workdir worker's tools
	//     carrier instead. Applied here 2026-07-05 — see
	//     brainstorms/abc-data-node/2026-07-04-aither-abc-tools-rw-worker-mount-report.md
	//     in abc-universe for the live-incident writeup this responds to.
	//     Tamper-safety for the binaries at that path still comes from them
	//     being root/operator-owned 0755, not from the RO mount flag.
	var vols []string
	hasWorkDirVol := false
	if !isS3URI(spec.WorkDir) && spec.HostVolume != "-" {
		vols = append(vols, fmt.Sprintf(`[type: "host", name: "%s", path: "%s"]`, hostVol, spec.WorkDir))
		hasWorkDirVol = true
	}
	if usesAbcTools(spec) {
		if hasWorkDirVol {
			vols = append(vols, `[type: "host", name: "abc-tools", path: "/nxf-work", readOnly: true]`)
		} else {
			// S3 workdir: use nf-work, not abc-tools — see the comment block above.
			vols = append(vols, `[type: "host", name: "nf-work", path: "/nxf-work"]`)
		}
	}
	volumesLine := "volumes = []"
	if len(vols) > 0 {
		volumesLine = "volumes = [" + strings.Join(vols, ", ") + "]"
	}

	// nf-nomad-s5cmd config block: emitted whenever the plugin is loaded (S3 or not).
	// Always pins binary = /nxf-work/bin/s5cmd (the abc-tools mount); the workDir
	// staging sub-block is added only for an S3 work dir.
	s5cmdBlock := ""
	if hasPlugin(spec.Plugins, "nf-nomad-s5cmd") {
		s5cmdBlock = buildS5cmdBlock(spec.WorkDir, spec.S5cmdSkipTLS)
	}

	// Per-process Nomad constraint via the `constraints` process directive.
	// Note: nf-nomad 0.4.0-edge3 requires the `constraints` value to be a Closure.
	// Nextflow's config-file parser converts `constraints { ... }` blocks to Maps,
	// so we MUST use property-assignment form (`= { ... }`) which preserves the closure.
	processConstraint := ""
	switch {
	case spec.NodeConstraint != "" && spec.PinWorkers:
		// Pin workers to the same node as the head (single-host run).
		processConstraint = fmt.Sprintf(`

process {
  constraints = { node { unique = [name: '%s'] } }
}
`, spec.NodeConstraint)
	case len(spec.WorkerExcludeHost) > 0:
		// Exclude workers from one or more specific nodes (forces head≠worker
		// placement). Uses nf-nomad's JobConstraintsNode.raw() which accepts
		// arbitrary operators (the typed DSL methods only emit '=', not '!=').
		// Multiple `raw` calls inside one `node { }` closure accumulate as
		// independent (AND'ed) constraints, so excluding N hosts emits N lines.
		var raws []string
		for _, host := range spec.WorkerExcludeHost {
			raws = append(raws, fmt.Sprintf(`raw 'unique.name', '!=', '%s'`, host))
		}
		processConstraint = fmt.Sprintf(`

process {
  constraints = { node { %s } }
}
`, strings.Join(raws, "; "))
	}

	// Build the plugins { ... } block. Default (no spec.Plugins) keeps the
	// historical single nf-nomad line. An empty NfPluginVersion renders the
	// bare `id "nf-nomad"` form (Nextflow resolves it to the newest published
	// release); a literal `@` suffix would be rejected by the plugin index.
	pluginsBody := `  id "nf-nomad"`
	if spec.NfPluginVersion != "" {
		pluginsBody = fmt.Sprintf(`  id "nf-nomad@%s"`, spec.NfPluginVersion)
	}
	if len(spec.Plugins) > 0 {
		var lines []string
		for _, p := range spec.Plugins {
			if p.Version != "" {
				lines = append(lines, fmt.Sprintf(`  id "%s@%s"`, p.ID, p.Version))
			} else {
				lines = append(lines, fmt.Sprintf(`  id "%s"`, p.ID))
			}
		}
		pluginsBody = strings.Join(lines, "\n")
	}

	// Container runtime block: docker (default), singularity, apptainer, or podman.
	// For singularity/apptainer with Wave, ociAutoPull lets the local runtime convert
	// the Wave OCI proxy image to SIF — no Wave-side SIF build required (Wave Lite safe).
	// For podman: nf-nomad 0.4.0-edge5 has a regression where it calls PodmanConfig.setEnabled()
	// which Nextflow 26.04.2 does not support. We therefore do NOT emit a podman {} block; instead
	// withContainerFlag is set to "-with-podman" and appended to the nextflow run command, which
	// enables Podman without going through the setEnabled code path.
	var containerBlock string
	switch strings.ToLower(spec.ContainerRuntime) {
	case "singularity":
		if spec.WaveEndpoint != "" {
			containerBlock = `singularity {
  enabled    = true
  ociAutoPull = true
}`
		} else {
			containerBlock = `singularity {
  enabled = true
}`
		}
	case "apptainer":
		if spec.WaveEndpoint != "" {
			containerBlock = `apptainer {
  enabled    = true
  ociAutoPull = true
}`
		} else {
			containerBlock = `apptainer {
  enabled = true
}`
		}
	case "podman":
		// nf-nomad always uses the Nomad Docker task driver for worker jobs
		// (isContainerNative() = true, JobBuilder hardcodes driver = "docker").
		// Emitting podman { enabled = true } triggers a Nextflow core bug (≥25.10.0):
		// TaskRun.setEnabled() is called on an immutable PodmanConfig (no setter).
		// Since nf-nomad ignores the podman config block for worker execution anyway,
		// we fall through to the docker default which works correctly.
		fallthrough
	default:
		containerBlock = `docker {
  enabled = true
}`
	}

	// Optional Wave and Fusion blocks.
	waveBlock := ""
	if spec.WaveEndpoint != "" {
		waveBlock = fmt.Sprintf(`
wave {
  enabled  = true
  endpoint = "%s"
}
`, spec.WaveEndpoint)
		if spec.FusionEnabled {
			waveBlock += `
fusion {
  enabled = true
}
`
		}
	}

	fmt.Fprintf(&sb, `plugins {
%s
}

%s

process {
  executor      = "nomad"
  errorStrategy = "retry"
  // Bumped 1→3: concurrent pipelines briefly overwhelm the single Nomad
  // server's per-client HTTP connection budget (all heads co-locate on the
  // platform node, sharing one client IP), surfacing as empty
  // io.nomadproject.client.ApiException on worker submit. More retries ride
  // out the transient burst. See abc-universe ADR (nf-nomad concurrent submit).
  maxRetries    = 3
}

executor {
  // Cap concurrent in-flight tasks + submission rate so a single pipeline
  // doesn't open an unbounded number of connections to the Nomad server.
  // With heads co-located on the platform node, several pipelines share the
  // server's per-client-IP connection budget (Nomad default
  // http_max_conns_per_client = 100), so each pipeline must stay modest.
  queueSize       = 50
  submitRateLimit = "10/1sec"
}

workDir = "%s"

aws {
  accessKey = System.getenv("AWS_ACCESS_KEY_ID") ?: ""
  secretKey = System.getenv("AWS_SECRET_ACCESS_KEY") ?: ""
  client {
    endpoint         = System.getenv("NF_MINIO_ENDPOINT") ?: "http://localhost:9000"
    s3PathStyleAccess = true
    protocol         = "https"
  }
}

nomad {
  client {
    address        = System.getenv("NOMAD_ADDR") ?: "http://127.0.0.1:4646"
    token          = System.getenv("NOMAD_TOKEN") ?: ""
    pollInterval   = "5s"
    submitThrottle = "100ms"
  }
  jobs {
    namespace                = "%s"
    deleteOnCompletion       = false
    cpuMode                  = "cores"
    privileged               = false
    failOnPlacementFailure   = true
    placementFailureTimeout  = "5m"
    %s
    %s
    %s
    %s
    failures = [
      restart   : [attempts: 1, mode: "fail"],
      reschedule: [attempts: 1]
    ]
  }
%s}
%s%s`, pluginsBody, containerBlock, spec.WorkDir, spec.Namespace, volumesLine, identityPassthroughLine(spec.Identity, spec.ExtraPassthroughEnvKeys...), secretPassthroughLine(spec.SecretPassthroughEnvKeys), workerNodePoolLine(spec.WorkerPool), s5cmdBlock, waveBlock, processConstraint)

	if spec.ExtraConfig != "" {
		sb.WriteString("\n")
		sb.WriteString(spec.ExtraConfig)
	}
	return sb.String()
}

// buildEntrypoint generates the bash entrypoint script for the head job.
func buildEntrypoint(spec Spec) string {
	var sb strings.Builder
	sb.WriteString("#!/usr/bin/env bash\nset -euo pipefail\ncd /local\n\n")
	// Mirror everything from now on into a debug log on the persistent host
	// volume. Nomad alloc logs are gated by ACL (alloc-fs read), so when a
	// run dies before producing a meaningful Nextflow log this is the only
	// post-mortem we get without a privileged token.
	sb.WriteString("DEBUG_LOG=\"" + spec.WorkDir + "/.head-debug-${NOMAD_ALLOC_ID:-noalloc}.log\"\n")
	sb.WriteString("mkdir -p \"$(dirname \"$DEBUG_LOG\")\" 2>/dev/null || true\n")
	sb.WriteString("exec > >(tee -a \"$DEBUG_LOG\") 2>&1\n")
	sb.WriteString("echo \"[head] start $(date -u +%Y-%m-%dT%H:%M:%SZ) alloc=${NOMAD_ALLOC_ID:-} job=${NOMAD_JOB_ID:-}\"\n\n")
	// NXF_HOME is task-local (under /local, the alloc's ephemeral task dir).
	// This avoids cross-run pollution of the plugins/ tree on shared host
	// volumes — observed empirically when an older bundle dropped a now-stale
	// "aggregator marker" dir into the persistent host volume that Nextflow's
	// PF4J loader then tripped over on subsequent runs. Per-alloc isolation
	// is the cheap, robust fix; the per-run cost (re-extracting the plugin
	// bundle) is dwarfed by image-pull time.
	nxfHome := "/local/.nxf-home"
	fmt.Fprintf(&sb, "export NXF_ANSI_LOG=false\nexport NXF_HOME=%s\n", nxfHome)
	// NXF_SYNTAX_PARSER=v1 forces the legacy (pre-25) config parser. Required
	// for pipelines that use bareword "$ENV_VAR" interpolation in nextflow.config
	// (e.g. nextflow-io/rnaseq-nf v2.3's Azure block). Nextflow 25+ defaults to
	// the strict v2 parser which rejects undefined identifiers at parse time.
	// Setting v1 is a no-op for pipelines that already use env('VAR') / params.
	sb.WriteString("export NXF_SYNTAX_PARSER=v1\n\n")

	// Move the auto-extracted plugin bundle into NXF_HOME/plugins before
	// invoking nextflow. Nomad's artifact stanza (with ?archive=zip) unpacks
	// the zip into local/plugins-bundle/<plugin>-<version>/... directly, so
	// we only need a recursive copy here — no `unzip` dependency in the image.
	if spec.PluginBundleURL != "" {
		fmt.Fprintf(&sb, "mkdir -p \"%s/plugins\"\n", nxfHome)
		fmt.Fprintf(&sb, "cp -r /local/plugins-bundle/. \"%s/plugins/\"\n\n", nxfHome)
	}

	// Prepend the custom Nextflow fork to PATH so it shadows the image binary.
	// The zip is extracted to local/nextflow-dev/ by the artifact stanza above.
	if spec.NextflowBinURL != "" {
		sb.WriteString("chmod +x /local/nextflow-dev/nextflow 2>/dev/null || true\n")
		sb.WriteString("export PATH=\"/local/nextflow-dev:$PATH\"\n\n")
	}

	// abc-tools host volume (mounted at /nxf-work): put its bin/ on PATH so s5cmd,
	// rclone, etc. resolve from the MOUNT — jobs never download tools (ADR-0061).
	if usesAbcTools(spec) {
		sb.WriteString("export PATH=\"/nxf-work/bin:$PATH\"\n\n")
	}

	// Make any tool binaries pulled by ExtraBinaries executable and on PATH.
	// (ExtraBinaries is now reserved for explicit operator-custom binaries; the
	// cluster tools s5cmd/rclone come from the abc-tools mount above, not artifacts.)
	if len(spec.ExtraBinaries) > 0 {
		sb.WriteString("for d in /local/bin-*/; do\n")
		sb.WriteString("  [ -d \"$d\" ] || continue\n")
		sb.WriteString("  chmod +x \"$d\"* 2>/dev/null || true\n")
		sb.WriteString("  export PATH=\"${d%/}:$PATH\"\n")
		sb.WriteString("done\n\n")
	}

	fmt.Fprintf(&sb, "nextflow run %s \\\n", spec.Repository)
	sb.WriteString("  -c /local/nextflow.headjob.config")
	// Explicit Nextflow run name. Required because the head sets
	// NXF_IGNORE_RESUME_HISTORY=true for S3-cloudcache work dirs (so
	// cross-container `-resume` works on a fresh head container). With the
	// history file disabled, Nextflow refuses to auto-generate a run name and
	// aborts with "Missing workflow run name" (CmdRun.checkRunName). The name
	// is a valid run name (^[a-z][a-z0-9_-]* …) and correlates the Nextflow
	// run with the Nomad job id. For resume lineages NextflowRunName carries
	// `<base>_<n>`; otherwise we fall back to the run tag.
	if runName := spec.NextflowRunName; runName != "" {
		fmt.Fprintf(&sb, " \\\n  -name %s", runName)
	} else if spec.RunTag != "" {
		fmt.Fprintf(&sb, " \\\n  -name %s", spec.RunTag)
	}
	if spec.Revision != "" {
		fmt.Fprintf(&sb, " \\\n  -revision %s", spec.Revision)
	}
	if spec.Profile != "" {
		fmt.Fprintf(&sb, " \\\n  -profile %s", spec.Profile)
	}
	// `-resume` is reserved for genuine Nextflow resume operations. SessionID
	// is the prior session the user wants to resume from (the resumed run's
	// UUID) — distinct from the run-tag emitted above as `-name`, which names
	// THIS (new) run. On a fresh head container resume also relies on
	// NXF_IGNORE_RESUME_HISTORY=true (set in hcl_adapter) to skip the local
	// run-history existence check; restore itself reads the per-task blobs
	// from the S3 cloudcache keyed by SessionID.
	if spec.PinnedSessionUUID != "" {
		// Always-resume a pinned session UUID (cloudcache runs). Makes the head
		// restart/reschedule-resilient: the static entrypoint re-runs the same
		// `-resume <uuid>` and reuses completed tasks. For a user resume the
		// UUID is the requested SessionID; for a fresh run it's a new UUID
		// (empty cache → behaves like a fresh run).
		fmt.Fprintf(&sb, " \\\n  -resume %s", spec.PinnedSessionUUID)
	} else if spec.Resume && spec.SessionID != "" {
		fmt.Fprintf(&sb, " \\\n  -resume %s", spec.SessionID)
	} else if spec.Resume {
		sb.WriteString(" \\\n  -resume")
	}
	if len(spec.Params) > 0 {
		sb.WriteString(" \\\n  -params-file /local/params.json")
	}
	sb.WriteString("\n")
	return sb.String()
}
