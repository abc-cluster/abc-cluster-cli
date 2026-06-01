// Package jobstage builds the data-staging plan for `abc job run <script>`:
// it decides which declared inputs must be uploaded vs are already in object
// storage, and generates the declarative s5cmd commands-files (stage-in /
// stage-out) plus the run-manifest. This is the cluster-independent core of
// spec abc-job-data-staging-and-run-tags Part A — no FUSE, no shell loops; the
// generated manifests are executed by `s5cmd run` in Nomad prestart/poststop
// tasks (wiring lives in cmd/job, completed against the live cluster).
//
// Design: $ABC_UNIVERSE/brainstorms/abc-data-platform/2026-06-01-granular-job-data-staging.md
package jobstage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// rootSentinels are the files that mark a project root, in priority order.
// PROJECT_ROOT.txt is the abc-project-garden marker; .abc-project.toml is the
// seedling template anchor; .git is the universal fallback.
var rootSentinels = []string{"PROJECT_ROOT.txt", ".abc-project.toml", ".git"}

// FindProjectRoot walks up from startDir until it finds a root sentinel.
// Returns the directory containing the sentinel. Inputs/outputs are made
// relative to this root so the staged tree on the node mirrors the project.
func FindProjectRoot(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		for _, s := range rootSentinels {
			if _, err := os.Stat(filepath.Join(dir, s)); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no project root found above %s (looked for %s)",
				startDir, strings.Join(rootSentinels, ", "))
		}
		dir = parent
	}
}

// InputKind classifies where a declared input already lives.
type InputKind int

const (
	// KindLocalOnly: a workbench-local file not yet in object storage — must be
	// uploaded to the per-run inputs prefix before the node can fetch it.
	KindLocalOnly InputKind = iota
	// KindInMinIO: already under the caller's user/<slot> prefix — referenced
	// by its bucket URI, not re-uploaded.
	KindInMinIO
	// KindCommon: a symlink resolving under the configured common mount — mapped
	// to the group common-bucket URI, downloaded on the node, not uploaded.
	KindCommon
)

func (k InputKind) String() string {
	switch k {
	case KindLocalOnly:
		return "local"
	case KindInMinIO:
		return "minio"
	case KindCommon:
		return "common"
	}
	return "unknown"
}

// CommonMount maps the local read-only geesefs mount (e.g. ~/common) to its
// backing bucket + prefix (e.g. s3://su-<group>/common/). Zero value (empty
// Path) disables common detection — symlinks then fall through to local/minio.
type CommonMount struct {
	Path   string // absolute local mount path, e.g. /home/<slot>/common
	Bucket string // backing bucket, e.g. su-mbhg-hostgen
	Prefix string // key prefix under the bucket, e.g. common/
}

// StagedInput is one resolved input in the staging plan.
type StagedInput struct {
	RelPath   string    // path relative to the project root (preserved on the node)
	AbsPath   string    // local absolute path (used for the upload of KindLocalOnly)
	Kind      InputKind // where it already lives
	BucketURI string    // s3://... source for Kind{InMinIO,Common}; "" for local until uploaded
}

// StagedOutput is one declared output (relative to project root).
type StagedOutput struct {
	RelPath string
}

// Plan is the full staging plan for one job run.
//
// Manifests use paths RELATIVE to the staged mirror root. The stage task cd's
// into DestRoot (the alloc-shared dir, e.g. "$NOMAD_ALLOC_DIR/data/<run>",
// resolved by the shell at runtime) before running s5cmd, so the relative
// local paths resolve there. DestRoot is therefore NOT embedded in the manifest
// (its absolute alloc path is unknown at generation time) — it is the task CWD,
// passed to the HCL emitter.
type Plan struct {
	ProjectRoot string
	RunPrefix   string // s3://<bucket>/user/<slot>/<proj>/jobs/<run>
	DestRoot    string // alloc-shared mirror root the stage tasks cd into (shell-expanded)
	Inputs      []StagedInput
	Outputs     []StagedOutput
}

// slotBucketPrefix is the caller's own area, e.g. ("su-mbhg-hostgen", "user/calm_dassie").
// ClassifyInput decides the kind of an input given the project + slot context.
//
//   - A symlink resolving under common.Path  → KindCommon (bucket = common bucket).
//   - A path already under s3://slotBucket/slotPrefix (passed as an s3:// URI or
//     a path the caller marks in-minio) → KindInMinIO.
//   - Anything else local → KindLocalOnly (uploaded to the per-run inputs prefix).
//
// inPath may be an s3:// URI (already-in-minio, referenced verbatim) or a local
// filesystem path (classified by symlink target / locality).
func ClassifyInput(inPath, projectRoot, slotBucket, slotPrefix string, common CommonMount) (StagedInput, error) {
	// Already an object-store URI → referenced as-is; rel path is the key tail.
	if strings.HasPrefix(inPath, "s3://") {
		return StagedInput{
			RelPath:   relFromS3(inPath),
			Kind:      KindInMinIO,
			BucketURI: inPath,
		}, nil
	}

	abs, err := filepath.Abs(inPath)
	if err != nil {
		return StagedInput{}, err
	}
	rel, err := filepath.Rel(projectRoot, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return StagedInput{}, fmt.Errorf("input %s is outside the project root %s", inPath, projectRoot)
	}

	// Symlink resolving under the common mount → common bucket. Resolve both
	// the mount path and the symlink target so platform-level symlinks in the
	// path (e.g. macOS /var → /private/var, or a symlinked mount root) don't
	// defeat the prefix check.
	if common.Path != "" {
		commonRoot, cerr := filepath.EvalSymlinks(common.Path)
		if cerr != nil {
			commonRoot = common.Path
		}
		if target, err := filepath.EvalSymlinks(abs); err == nil {
			if sub, ok := underDir(commonRoot, target); ok {
				uri := joinS3(common.Bucket, common.Prefix, sub)
				return StagedInput{RelPath: rel, AbsPath: abs, Kind: KindCommon, BucketURI: uri}, nil
			}
		}
	}

	// Default: local-only — uploaded to the per-run inputs prefix.
	return StagedInput{RelPath: rel, AbsPath: abs, Kind: KindLocalOnly}, nil
}

// LocalUploads returns the (absPath → destURI) pairs the CLI must `abc data
// push` to the per-run inputs prefix before submit. destURI preserves RelPath.
func (p Plan) LocalUploads() [][2]string {
	var out [][2]string
	for _, in := range p.Inputs {
		if in.Kind == KindLocalOnly {
			out = append(out, [2]string{in.AbsPath, p.RunPrefix + "/inputs/" + in.RelPath})
		}
	}
	return out
}

// StageInManifest returns the s5cmd commands-file that downloads every input
// into DestRoot, preserving the relative tree. Local-only inputs are fetched
// from the per-run inputs prefix (where the CLI uploaded them); in-minio/common
// inputs are fetched from their existing bucket URI. Deterministic ordering.
func (p Plan) StageInManifest() string {
	lines := make([]string, 0, len(p.Inputs))
	for _, in := range p.Inputs {
		src := in.BucketURI
		if in.Kind == KindLocalOnly {
			src = p.RunPrefix + "/inputs/" + in.RelPath
		}
		// Destination is RELATIVE to the staged mirror (DestRoot, the task CWD).
		lines = append(lines, fmt.Sprintf("cp %s %s", src, in.RelPath))
	}
	sort.Strings(lines)
	return header("stage-in") + strings.Join(lines, "\n") + "\n"
}

// StageOutManifest returns the s5cmd commands-file that uploads each declared
// output (relative to DestRoot) to the per-run outputs prefix, preserving the
// relative tree. A trailing "/" on an output RelPath denotes a directory
// (recursive upload).
func (p Plan) StageOutManifest() string {
	lines := make([]string, 0, len(p.Outputs))
	for _, o := range p.Outputs {
		rel := o.RelPath
		// Source is RELATIVE to the staged mirror (DestRoot, the task CWD).
		src := rel
		dst := p.RunPrefix + "/outputs/" + rel
		if strings.HasSuffix(rel, "/") {
			// directory: recursive upload preserving structure
			src = strings.TrimSuffix(rel, "/") + "/*"
		}
		lines = append(lines, fmt.Sprintf("cp %s %s", src, dst))
	}
	sort.Strings(lines)
	return header("stage-out") + strings.Join(lines, "\n") + "\n"
}

// CleanManifest returns an s5cmd commands-file that removes the per-run prefix
// (the explicit `--clean` path; the default is a MinIO ILM lifecycle rule).
func (p Plan) CleanManifest() string {
	return header("clean") + fmt.Sprintf("rm %s/*\n", p.RunPrefix)
}

// RunManifest is the lineage record written to <RunPrefix>/run-manifest.json
// (mirrors the nf-nomad pattern) and is also the basis for the local.db row.
type RunManifest struct {
	RunID           string   `json:"run_id"`
	Script          string   `json:"script"`
	PixiRef         string   `json:"pixi_ref,omitempty"`
	InvestigationID string   `json:"investigation_id,omitempty"`
	Inputs          []string `json:"inputs"`  // source URIs (per-run inputs prefix or existing bucket URIs)
	Outputs         []string `json:"outputs"` // destination URIs under the outputs prefix
	Tags            []string `json:"tags,omitempty"`
}

// RunManifest builds the lineage record for this plan.
func (p Plan) RunManifest(runID, script, pixiRef, investigationID string, tags []string) RunManifest {
	m := RunManifest{
		RunID:           runID,
		Script:          script,
		PixiRef:         pixiRef,
		InvestigationID: investigationID,
		Tags:            tags,
	}
	for _, in := range p.Inputs {
		if in.Kind == KindLocalOnly {
			m.Inputs = append(m.Inputs, p.RunPrefix+"/inputs/"+in.RelPath)
		} else {
			m.Inputs = append(m.Inputs, in.BucketURI)
		}
	}
	for _, o := range p.Outputs {
		m.Outputs = append(m.Outputs, p.RunPrefix+"/outputs/"+o.RelPath)
	}
	return m
}

// MarshalJSON renders the manifest with stable, indented output.
func (m RunManifest) Marshal() ([]byte, error) { return json.MarshalIndent(m, "", "  ") }

// ----- helpers -----

func header(kind string) string {
	return fmt.Sprintf("# abc job-staging %s manifest — executed by `s5cmd run`\n", kind)
}

// underDir reports whether target is dir or a descendant of dir, returning the
// sub-path relative to dir.
func underDir(dir, target string) (string, bool) {
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return rel, true
}

// relFromS3 returns the last path segment of an s3:// URI (used as the RelPath
// for already-in-MinIO inputs so they land sensibly in the node mirror).
func relFromS3(uri string) string {
	trimmed := strings.TrimPrefix(uri, "s3://")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 && i+1 < len(trimmed) {
		return trimmed[i+1:]
	}
	return trimmed
}

func joinS3(bucket, prefix, sub string) string {
	prefix = strings.Trim(prefix, "/")
	sub = strings.TrimPrefix(sub, "/")
	if prefix == "" {
		return fmt.Sprintf("s3://%s/%s", bucket, sub)
	}
	return fmt.Sprintf("s3://%s/%s/%s", bucket, prefix, sub)
}
