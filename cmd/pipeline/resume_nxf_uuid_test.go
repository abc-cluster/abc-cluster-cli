package pipeline

import (
	"strings"
	"testing"
)

// NXF_UUID must equal the -resume UUID so Nextflow reads AND writes the
// cloudcache under the same session namespace. Without this, every run writes
// to a fresh random namespace and resume produces only partial cache hits.
func TestHCLAdapter_NXFUUIDMatchesResumeUUID(t *testing.T) {
	const canonicalWD = "s3://su-mbhg-hostgen/user/solar-civet/workdir/solar-civet-1780683367/"
	spec := &PipelineSpec{
		Repository:  "https://github.com/TORCH-Consortium/MAGMA",
		WorkDir:     canonicalWD,
		Datacenters: []string{"seedling-prod"},
	}
	spec.defaults()
	// Simulate the session UUID derivation that run.go performs before HCL generation.
	spec.PinnedSessionUUID = deterministicSessionUUID(canonicalWD)

	// Use the full top-level function (hcl_adapter code path) — that's where
	// NXF_UUID is injected from spec.PinnedSessionUUID into staticEnv.
	hcl := generateHeadJobHCL(spec, "http://127.0.0.1:4646", "token", "run-uuid")

	// Both NXF_UUID and -resume must be present and identical.
	wantUUID := spec.PinnedSessionUUID
	if wantUUID == "" {
		t.Fatal("PinnedSessionUUID is empty — deterministicSessionUUID did not return a value")
	}
	// env block aligns values with padding; match key prefix + quoted value.
	if !strings.Contains(hcl, `NXF_UUID`) || !strings.Contains(hcl, `"`+wantUUID+`"`) {
		t.Errorf("HCL does not contain NXF_UUID = %q\n--- HCL excerpt ---\n%s",
			wantUUID, extractEnvBlock(hcl))
	}
	resumeFlag := "-resume " + wantUUID
	if !strings.Contains(hcl, resumeFlag) {
		t.Errorf("HCL does not contain %q", resumeFlag)
	}
}

// A fresh run (no --resume) must also pin NXF_UUID so that the run writes its
// cache under the deterministic namespace and future resumes find it.
func TestHCLAdapter_FreshRunPinsNXFUUID(t *testing.T) {
	const wd = "s3://su-bucket/user/me/workdir/me-1234567890/"
	spec := &PipelineSpec{
		Repository:  "TORCH-Consortium/MAGMA",
		WorkDir:     wd,
		Datacenters: []string{"dc1"},
	}
	spec.defaults()
	spec.PinnedSessionUUID = deterministicSessionUUID(wd)
	// Not setting spec.Resume → simulates fresh run

	hcl := generateHeadJobHCL(spec, "http://127.0.0.1:4646", "t", "r")
	if !strings.Contains(hcl, "NXF_UUID") || !strings.Contains(hcl, `"`+spec.PinnedSessionUUID+`"`) {
		t.Errorf("fresh run HCL missing NXF_UUID; PinnedSessionUUID=%s", spec.PinnedSessionUUID)
	}
}

// Non-canonical work dirs (no cloudcache) must NOT emit NXF_UUID (they use the
// local LevelDB and random sessions are the expected behaviour).
func TestHCLAdapter_NonCanonicalWorkDirOmitsNXFUUID(t *testing.T) {
	spec := &PipelineSpec{
		Repository:  "nextflow-io/hello",
		WorkDir:     "/scratch/local/nf-work/",
		Datacenters: []string{"dc1"},
	}
	spec.defaults()
	// PinnedSessionUUID not set for non-canonical work dirs (run.go gate)

	hcl := generateHeadJobHCL(spec, "http://127.0.0.1:4646", "t", "r")
	if strings.Contains(hcl, "NXF_UUID") {
		t.Errorf("non-canonical work dir should not emit NXF_UUID, but it does")
	}
}

// extractEnvBlock returns a short excerpt of the HCL for test error messages.
func extractEnvBlock(hcl string) string {
	start := strings.Index(hcl, "env {")
	if start < 0 {
		return "(no env block)"
	}
	end := strings.Index(hcl[start:], "}")
	if end < 0 {
		return hcl[start:]
	}
	return hcl[start : start+end+1]
}
