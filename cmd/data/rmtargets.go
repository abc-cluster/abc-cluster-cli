package data

import (
	"fmt"
	"strings"
)

// s5cmd refuses a delete against a bare bucket/prefix — it wants an explicit
// glob ("s3 bucket/prefix cannot be used for delete operations (forgot wildcard
// character?)"). Both `abc data remove` and `abc data purge` pass user arguments
// straight through, so a natural `--recursive` invocation on a prefix failed with
// s5cmd's message instead of doing what the flag says.
//
// expandRecursiveTargets turns prefix-shaped arguments into the globbed form
// s5cmd needs, and refuses a bare prefix when --recursive was NOT given, so the
// error names the flag the caller wants rather than s5cmd's wildcard trivia.

// isPrefixTarget reports whether uri names a prefix rather than a single object:
// it ends in "/" (e.g. s3://bucket/path/) or is a bare bucket (s3://bucket).
// A target that already carries a glob is left alone — the caller meant it.
func isPrefixTarget(uri string) bool {
	if strings.ContainsAny(uri, "*?") {
		return false
	}
	if strings.HasSuffix(uri, "/") {
		return true
	}
	rest := strings.TrimPrefix(uri, "s3://")
	return rest != "" && !strings.Contains(rest, "/")
}

// expandRecursiveTargets prepares delete targets for s5cmd.
//
// With recursive set, a prefix target gains the trailing glob s5cmd requires:
// "s3://b/p/" becomes "s3://b/p/*". The glob is anchored to that prefix, so it
// can never match a sibling — "s3://b/p/*" does not reach "s3://b/p-other/".
// Targets that already carry a glob, and single-object targets, pass through
// untouched.
//
// Without recursive, a prefix target is rejected up front: deleting a whole
// prefix is exactly what --recursive is for, and letting it through only
// produces s5cmd's "forgot wildcard character?" further down.
func expandRecursiveTargets(args []string, recursive bool) ([]string, error) {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if !isPrefixTarget(a) {
			out = append(out, a)
			continue
		}
		if !recursive {
			return nil, fmt.Errorf(
				"%q is a prefix, not an object — pass --recursive to remove everything under it", a)
		}
		if strings.HasSuffix(a, "/") {
			out = append(out, a+"*")
		} else {
			out = append(out, a+"/*")
		}
	}
	return out, nil
}
