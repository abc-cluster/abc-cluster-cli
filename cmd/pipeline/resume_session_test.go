package pipeline

import (
	"regexp"
	"testing"
)

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// The core resume guarantee: a fresh run's work-dir and a `--work-dir` resume of
// it must derive the SAME session UUID, so they share one cloudcache namespace
// (`cache/<run-tag>/<session-uuid>/`) and `-resume` reuses completed tasks.
func TestDeterministicSessionUUID_StableAndShared(t *testing.T) {
	const fresh = "s3://su-mbhg-hostgen/user/solar-civet/workdir/solar-civet-1780654236/"
	// Same lineage, resume often passes the path without a trailing slash.
	const resumeNoSlash = "s3://su-mbhg-hostgen/user/solar-civet/workdir/solar-civet-1780654236"

	a := deterministicSessionUUID(fresh)
	b := deterministicSessionUUID(fresh)
	c := deterministicSessionUUID(resumeNoSlash)

	if !uuidShape.MatchString(a) {
		t.Fatalf("not a UUID shape: %q", a)
	}
	if a != b {
		t.Errorf("not deterministic: %q != %q", a, b)
	}
	if a != c {
		t.Errorf("trailing slash changed the UUID: %q (with /) != %q (without /) — would break resume", a, c)
	}
}

func TestDeterministicSessionUUID_DistinctPerRun(t *testing.T) {
	x := deterministicSessionUUID("s3://su-mbhg-hostgen/user/solar-civet/workdir/solar-civet-1780654236/")
	y := deterministicSessionUUID("s3://su-mbhg-hostgen/user/solar-civet/workdir/solar-civet-1780678879/")
	if x == y {
		t.Errorf("different runs must get different sessions, got %q for both", x)
	}
}

// Non-canonical work dirs have no cloudcache; the value is unused for resume but
// must still be a valid (random) UUID shape, matching prior behaviour.
func TestDeterministicSessionUUID_NonCanonicalFallback(t *testing.T) {
	for _, wd := range []string{"", "/scratch/local/nf-work/foo/", "s3://bucket/not/canonical/here/"} {
		got := deterministicSessionUUID(wd)
		if !uuidShape.MatchString(got) {
			t.Errorf("deterministicSessionUUID(%q) = %q; want UUID shape", wd, got)
		}
	}
}
