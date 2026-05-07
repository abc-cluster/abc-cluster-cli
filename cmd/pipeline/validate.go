package pipeline

import (
	"fmt"
	"strings"
)

// validatePipelineRef rejects pipeline arguments that are clearly local paths
// or script files. abc pipeline run only supports GitHub repository references —
// either the short owner/repo form (e.g. nf-core/demo) or a full https:// URL.
//
// Future tool integrations (Snakemake, Airflow, Prefect) will add their own
// pipeline-type routing once supported; for now every submission is assumed to
// be a Nextflow pipeline, so local .nf files and shell scripts are rejected at
// this layer rather than surfacing as a confusing Nomad job failure.
func validatePipelineRef(ref string) error {
	// Absolute local path.
	if strings.HasPrefix(ref, "/") {
		return fmt.Errorf(
			"pipeline ref %q looks like an absolute local path\n"+
				"  abc pipeline run only accepts GitHub repository references:\n"+
				"    Short form:  abc pipeline run nf-core/demo\n"+
				"    Full URL:    abc pipeline run https://github.com/nf-core/demo\n"+
				"    Saved name:  abc pipeline run rnaseq",
			ref,
		)
	}

	// Relative local path.
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		return fmt.Errorf(
			"pipeline ref %q looks like a relative local path\n"+
				"  abc pipeline run only accepts GitHub repository references:\n"+
				"    Short form:  abc pipeline run nf-core/demo\n"+
				"    Full URL:    abc pipeline run https://github.com/nf-core/demo\n"+
				"    Saved name:  abc pipeline run rnaseq",
			ref,
		)
	}

	// Script/pipeline file extensions — any of these indicate a local file,
	// not a repository reference.
	lower := strings.ToLower(ref)
	for _, ext := range []string{".nf", ".sh", ".py", ".wl", ".smk", ".r"} {
		if strings.HasSuffix(lower, ext) {
			return fmt.Errorf(
				"pipeline ref %q looks like a local script file (%s)\n"+
					"  abc pipeline run does not accept local files; provide a GitHub repository reference:\n"+
					"    Short form:  abc pipeline run nf-core/demo\n"+
					"    Full URL:    abc pipeline run https://github.com/nf-core/demo",
				ref, ext,
			)
		}
	}

	// Unsupported URL scheme — reject anything with ://  that isn't https or http.
	if i := strings.Index(ref, "://"); i > 0 {
		scheme := strings.ToLower(ref[:i])
		if scheme != "https" && scheme != "http" {
			return fmt.Errorf(
				"unsupported URL scheme %q in pipeline ref %q\n"+
					"  abc pipeline run accepts https:// GitHub URLs or short owner/repo form",
				scheme, ref,
			)
		}
		// Full URL — no further checks needed.
		return nil
	}

	// Bare hostname form (e.g. github.com/nf-core/demo) — Nextflow does not
	// support this; only the short owner/repo form or a full https:// URL work.
	// Detect by checking whether the first path component (text before the first
	// "/" or the whole ref if no "/") contains a dot.
	firstComponent := ref
	if i := strings.Index(ref, "/"); i >= 0 {
		firstComponent = ref[:i]
	}
	if strings.Contains(firstComponent, ".") {
		return fmt.Errorf(
			"pipeline ref %q looks like a bare hostname URL (not supported by Nextflow)\n"+
				"  Use the full https:// form instead:\n"+
				"    abc pipeline run https://github.com/nf-core/demo\n"+
				"    abc pipeline run https://gitlab.com/owner/repo",
			ref,
		)
	}

	return nil
}
