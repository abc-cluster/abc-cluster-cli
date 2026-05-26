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
