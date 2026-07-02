package pipeline

import (
	"fmt"
	"strings"
)

// Bucket-layout conventions for `abc pipeline run`. Source spec:
//   abc-universe/specs/active/abc-bucket-layout-phase-1.md
//
// Each pipeline run gets a user-rooted, scope-prefixed home under the
// group bucket:
//
//   s3://<group-bucket>/<scope>/<user>/workdir/<run-tag>/
//   s3://<group-bucket>/<scope>/<user>/results/<run-tag>/
//
// scope is "user" (default; private) or "group" (--share opt-in;
// group-readable).

// scopeUser is the default visibility scope — owner + admins only.
const scopeUser = "user"

// scopeGroup is the opt-in visibility scope — owner writes, group reads.
const scopeGroup = "group"

// validScopes enumerates the accepted --visibility values. v1 supports
// two; the slice is sorted for stable error-message formatting.
var validScopes = []string{scopeGroup, scopeUser}

// derivedWorkDir returns the auto-derived Nextflow work directory for the
// given (bucket, scope, user, runTag). All four parameters must be
// non-empty; callers validate upstream and surface typed errors.
//
// Trailing slash is included so the URI is unambiguously a directory
// prefix (matches nf-nomad-s5cmd's existing handling of bucket+prefix).
func derivedWorkDir(bucket, scope, user, runTag string) string {
	return fmt.Sprintf("s3://%s/%s/%s/workdir/%s/", bucket, scope, user, runTag)
}

// derivedOutDir returns the auto-derived publishDir target. Symmetric to
// derivedWorkDir — pipelines that accept params.outdir (every nf-core
// pipeline, many community ones) get this injected when the user hasn't
// supplied one explicitly.
func derivedOutDir(bucket, scope, user, runTag string) string {
	return fmt.Sprintf("s3://%s/%s/%s/results/%s/", bucket, scope, user, runTag)
}

// resolveScope returns the visibility scope to use for derived paths.
// Precedence:
//  1. --visibility=<scope>  (explicit string flag wins; validated)
//  2. --share               (boolean shortcut for visibility=group)
//  3. default scopeUser
//
// An invalid --visibility value returns a typed error so the CLI can
// fail submit-time rather than emit a bogus path.
func resolveScope(visibility string, share bool) (string, error) {
	v := strings.TrimSpace(strings.ToLower(visibility))
	if v != "" {
		switch v {
		case scopeUser, scopeGroup:
			return v, nil
		default:
			return "", fmt.Errorf("--visibility: invalid value %q (must be one of %s)", visibility, strings.Join(validScopes, ", "))
		}
	}
	if share {
		return scopeGroup, nil
	}
	return scopeUser, nil
}

// bucketFromS3URI returns the bucket component of an "s3://bucket/..."
// URI. Returns "" when the input doesn't have the s3:// scheme. Used
// for soft validation of explicit --work-dir against the active group
// bucket.
func bucketFromS3URI(uri string) string {
	const prefix = "s3://"
	if !strings.HasPrefix(uri, prefix) {
		return ""
	}
	rest := uri[len(prefix):]
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

// deriveCloudCachePath returns the Nextflow CloudCache base path that
// `NXF_CLOUDCACHE_PATH` should point at, given a canonical CLI work-dir
// of the form `s3://<bucket>/<scope>/<user>/workdir/<run-tag>/`.
//
// Returned cache path:  `s3://<bucket>/<scope>/<user>/cache/<run-tag>/`
//   — sibling of workdir/ and results/ under the same scope+user prefix
//   — inherits the user's existing per-bucket IAM (no shared-write
//     surface; abuse vector limited to other users in the SAME group,
//     which the group already trusts)
//   — per-run-tag base ensures Nextflow's `<base>/history` is unique
//     per run, so simultaneous runs by the same user never race
//
// Returns "" when the work-dir doesn't match the canonical pattern
// (e.g. operator-supplied `--work-dir s3://other/...`) — the caller
// then omits NXF_CLOUDCACHE_PATH and Nextflow falls back to its
// default local-fs LevelDB.
//
// Resume semantics: when the caller passes `--work-dir <prev>` to
// resume an earlier run, this function parses <prev>'s components
// (bucket / scope / user / run-tag) and yields the SAME cache path
// the earlier run wrote to. Nextflow reads it back and the cached
// tasks skip on the resume run. No special-casing needed.
func deriveCloudCachePath(workDir string) string {
	const s3Prefix = "s3://"
	if !strings.HasPrefix(workDir, s3Prefix) {
		return ""
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(workDir, s3Prefix), "/")
	parts := strings.Split(rest, "/")
	// Canonical shape: <bucket>/<scope>/<user>/workdir/<run-tag>
	// → 5 parts; parts[3] MUST be the literal "workdir".
	if len(parts) < 5 || parts[3] != "workdir" {
		return ""
	}
	bucket, scope, user, runTag := parts[0], parts[1], parts[2], parts[4]
	if bucket == "" || scope == "" || user == "" || runTag == "" {
		return ""
	}
	return fmt.Sprintf("s3://%s/%s/%s/cache/%s/", bucket, scope, user, runTag)
}

// errNoGroupBucketForS3WorkDir is returned by preflightWorkDirDerivation
// when the command must auto-derive an S3 work-dir but the active context
// yields no group bucket (a group-less user). The message is actionable:
// the user either passes an explicit --work-dir or joins a group. Formatting
// the group name as "none" (rather than the empty string) keeps the error
// readable when bucket == "".
func errNoGroupBucketForS3WorkDir(bucket string) error {
	group := strings.TrimSpace(bucket)
	if group == "" {
		group = "none"
	}
	return fmt.Errorf(
		"cannot auto-derive an S3 work-dir: your user has no group bucket (group: %s). "+
			"Pass --work-dir s3://<accessible-bucket>/<path>/ explicitly, or join a group.",
		group,
	)
}

// preflightWorkDirDerivation gates the silent fall-through to the legacy
// host-volume default (/work/nextflow-work → s3://nextflow-work/) that
// happens when a group-less user runs a pipeline against an S3-work-dir
// context. It returns a non-nil error ONLY for the specific broken
// combination:
//
//   - workDirExplicit == false  (the user did NOT pass --work-dir), AND
//   - contextUsesS3WorkDir == true  (the context's work-dir lives in S3 —
//     i.e. an S3/MinIO/RustFS endpoint is configured), AND
//   - groupBucket == ""  (auto-derivation has no bucket to root the path).
//
// In every other case it returns nil and leaves resolution untouched:
//   - an explicit --work-dir (S3 or local) is always honoured;
//   - a non-S3 / host-volume context (no S3 endpoint) legitimately falls
//     back to /work/nextflow-work, so an empty bucket is fine there.
//
// This is a pure derivation check — no live S3 call. The failure is
// deterministic from "auto-derive requested + S3 context + no group
// bucket", which is more reliable than probing S3 credentials that may
// differ between the CLI and the head job.
func preflightWorkDirDerivation(workDirExplicit, contextUsesS3WorkDir bool, groupBucket string) error {
	if workDirExplicit {
		return nil
	}
	if !contextUsesS3WorkDir {
		return nil
	}
	if strings.TrimSpace(groupBucket) != "" {
		return nil
	}
	return errNoGroupBucketForS3WorkDir(groupBucket)
}

// validateExplicitWorkDir returns a warning string when the explicit
// --work-dir bucket doesn't align with the active context's group
// bucket. Empty warning + ok=true means the path is fine; non-empty
// warning + ok=true means proceed-with-warning (v1 is soft).
//
// v1 never returns ok=false — there is no Khan service yet to harden
// this. v2 may flip ok=false when --i-know-what-im-doing is absent.
func validateExplicitWorkDir(workDir, expectedBucket string) (warning string, ok bool) {
	if workDir == "" {
		return "", true
	}
	if !strings.HasPrefix(workDir, "s3://") {
		return fmt.Sprintf("--work-dir %q is not an s3:// URI; bucket-layout conventions apply to s3:// work dirs only", workDir), true
	}
	bucket := bucketFromS3URI(workDir)
	if bucket == "" {
		return fmt.Sprintf("--work-dir %q has no bucket component", workDir), true
	}
	if expectedBucket == "" {
		// Active context didn't yield a group bucket — can't validate.
		// Silently accept; the work-dir will either work or fail at submit.
		return "", true
	}
	if bucket != expectedBucket {
		return fmt.Sprintf(
			"--work-dir bucket s3://%s/... is outside the active context's group bucket (s3://%s/). "+
				"Workers may fail to write outputs if MinIO IAM rejects cross-bucket access.",
			bucket, expectedBucket,
		), true
	}
	return "", true
}
