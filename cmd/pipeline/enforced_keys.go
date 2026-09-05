package pipeline

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// enforcedConfigKeys are the settings abc must guarantee: identity, endpoint,
// tenancy boundary, data layout, and the shared Nomad server's connection
// budget. A run whose config does not mean what its author wrote is worse than
// a run that refuses to start, so setting one of these is an error rather than
// something quietly overridden.
//
// Deliberately NOT listed: queueSize, cpuMode, failOnPlacementFailure,
// maxRetries, errorStrategy and the rest of the workload's shape. Those are the
// pipeline's business and live in the overridable defaults.
//
// See design/exploring/abc-cluster-cli-config-precedence-tiers.md.
var enforcedConfigKeys = map[string]string{
	"process.executor":         "the Nomad executor is what makes this a cluster run",
	"workDir":                  "abc owns the work-dir layout that lineage, resume and the cloudcache key off",
	"nomad.client.address":     "the cluster endpoint is resolved from your abc context",
	"nomad.client.token":       "credentials are injected from your abc context",
	"nomad.jobs.namespace":     "the namespace is your tenancy boundary",
	"nomad.jobs.privileged":    "privileged containers are not permitted",
	"executor.submitRateLimit": "submission rate protects other runs sharing this Nomad server",
	"aws.accessKey":            "credentials are injected from your abc context",
	"aws.secretKey":            "credentials are injected from your abc context",
	"aws.client.endpoint":      "the object-store endpoint is resolved from your abc context",
}

// assignmentRe matches a Groovy config assignment for a dotted key, e.g.
//
//	workDir = "s3://..."
//	process.executor = 'nomad'
//
// Comments are skipped by the caller, so a key merely mentioned in prose does
// not trip the guard.
func assignmentRe(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=`)
}

// blockAssignmentRe matches the same key expressed as nesting, e.g.
//
//	nomad { client { address = "..." } }
//
// It is intentionally loose: it looks for the leaf assignment anywhere under a
// block whose name matches the key's first segment. False positives here cost a
// clear error message; false negatives cost a run that silently means something
// else.
func blockAssignmentRe(key string) *regexp.Regexp {
	parts := strings.Split(key, ".")
	leaf := parts[len(parts)-1]
	if len(parts) == 1 {
		return assignmentRe(leaf)
	}
	return regexp.MustCompile(`(?ms)\b` + regexp.QuoteMeta(parts[0]) + `\s*\{.*?\b` + regexp.QuoteMeta(leaf) + `\s*=`)
}

// stripConfigComments removes // line comments and /* */ blocks so a key named
// in an explanatory comment does not look like an assignment.
func stripConfigComments(s string) string {
	s = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(s, "")
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// findEnforcedOverrides reports which enforced keys a user-supplied config
// assigns, with the reason each is enforced.
func findEnforcedOverrides(config string) map[string]string {
	body := stripConfigComments(config)
	if strings.TrimSpace(body) == "" {
		return nil
	}
	hits := map[string]string{}
	for key, why := range enforcedConfigKeys {
		if assignmentRe(key).MatchString(body) || blockAssignmentRe(key).MatchString(body) {
			hits[key] = why
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return hits
}

// checkEnforcedOverrides fails the submit when a user config sets something abc
// must guarantee. The previous behaviour was to accept the file and let the
// generated config win, so the run started meaning something other than what
// was written.
func checkEnforcedOverrides(config, source string) error {
	hits := findEnforcedOverrides(config)
	if len(hits) == 0 {
		return nil
	}
	keys := make([]string, 0, len(hits))
	for k := range hits {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "%s sets %d value(s) that abc must control:\n", source, len(keys))
	for _, k := range keys {
		fmt.Fprintf(&b, "    %-26s %s\n", k, hits[k])
	}
	b.WriteString("\n  These are not overridable. Concurrency, resources and retry behaviour\n")
	b.WriteString("  (queueSize, cpuMode, failOnPlacementFailure, maxRetries, errorStrategy)\n")
	b.WriteString("  are — set those in your pipeline's nextflow.config or via --config.")
	return fmt.Errorf("%s", b.String())
}
