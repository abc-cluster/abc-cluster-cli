package job

import (
	"strings"
	"testing"
)

// TestGenerate_StagingDisabled: no staging fields → no staging volume/tasks
// (bare `abc job run` is unchanged).
func TestGenerate_StagingDisabled(t *testing.T) {
	out := Generate(Spec{Name: "j", Driver: "exec", Nodes: 1, Priority: 50}, "run.sh", "echo hi")
	for _, bad := range []string{"stage-in", "stage-out", "nf-work"} {
		if strings.Contains(out, bad) {
			t.Errorf("staging-disabled output unexpectedly contains %q:\n%s", bad, out)
		}
	}
}

// TestGenerate_StagingEnabled verifies spec abc-job-data-staging Part A criteria
// that are checkable in the emitted HCL without a cluster: A3 (three-phase:
// prestart stage-in + main + poststop stage-out), A4 (sidecar=false so prestart
// failure aborts main), and the nf-work host volume + mount (A9).
func TestGenerate_StagingEnabled(t *testing.T) {
	spec := Spec{
		Name: "penguins-rf", Driver: "exec", Nodes: 1, Priority: 50,
		Staging: StagingSpec{
			Enabled:          true,
			StageInManifest:  "cp s3://b/common/penguins.csv /alloc/data/r1/analysis/data/external/penguins.csv\n",
			StageOutManifest: "cp /alloc/data/r1/analysis/data/06_models/rf.pkl s3://b/user/s/p/jobs/r1/outputs/analysis/data/06_models/rf.pkl\n",
			DestRoot:         "${NOMAD_ALLOC_DIR}/data/r1",
			S5cmdPath:        "/nxf-work/bin/s5cmd",
			HostVolumeName:   "nf-work",
			HostVolumeSource: "/opt/abc-seedling/nf-work",
			HostVolumeMount:  "/nxf-work",
			Env:              map[string]string{"AWS_ACCESS_KEY_ID": "AKIA", "S3_ENDPOINT_URL": "https://s3.seedling"},
		},
	}
	out := Generate(spec, "run.sh", "python rf_train.py")

	// A3 — the three phases exist.
	for _, want := range []string{
		`task "stage-in"`,
		`task "main"`,
		`task "stage-out"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// Normalize hclwrite's "=" alignment padding so assertions are
	// whitespace-insensitive (hclwrite pads keys within a block to align "=").
	norm := strings.Join(strings.Fields(out), " ")
	contains := func(want string) {
		t.Helper()
		if !strings.Contains(norm, want) {
			t.Errorf("missing %q in normalized HCL:\n%s", want, out)
		}
	}

	// A3 — lifecycle hooks: stage-in=prestart, stage-out=poststop.
	contains(`hook = "prestart"`)
	contains(`hook = "poststop"`)

	// A4 — sidecar=false on the stage tasks (prestart failure aborts main).
	if strings.Count(norm, "sidecar = false") < 2 {
		t.Errorf("expected sidecar=false on both stage tasks:\n%s", out)
	}

	// A9 — nf-work host volume declared + mounted; s5cmd run drives the manifest.
	for _, want := range []string{
		`volume "nf-work"`,
		`source = "/opt/abc-seedling/nf-work"`,
		`volume_mount {`,
		`destination = "/nxf-work"`,
		`command = "/nxf-work/bin/s5cmd"`,
		`args = ["run", "local/stage-in.txt"]`,
		`args = ["run", "local/stage-out.txt"]`,
		`destination = "secrets/s5cmd.env"`, // creds injected as env
		`destination = "local/stage-in.txt"`,
		`destination = "local/stage-out.txt"`,
	} {
		contains(want)
	}
}
