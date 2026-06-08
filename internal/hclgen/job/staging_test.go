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
			StageInManifest:  "cp s3://b/common/penguins.csv analysis/data/external/penguins.csv\n",
			StageOutManifest: "cp analysis/data/06_models/rf.pkl s3://b/user/s/p/jobs/r1/outputs/analysis/data/06_models/rf.pkl\n",
			DestRoot:         "$NOMAD_ALLOC_DIR/data/r1",
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
		`source = "nf-work"`, // ADR-0061: source is the registered host_volume NAME, not its path
		`volume_mount {`,
		`destination = "/nxf-work"`,
		`command = "/bin/sh"`,
		`/nxf-work/bin/s5cmd run`,         // s5cmd invoked inside the sh wrapper
		`$NOMAD_ALLOC_DIR/data/r1`,        // cd into the alloc-shared DestRoot
		`$NOMAD_TASK_DIR/local/stage-in.txt`,
		`$NOMAD_TASK_DIR/local/stage-out.txt`,
		`destination = "secrets/s5cmd.env"`, // creds injected as env
		`destination = "local/stage-in.txt"`,
		`destination = "local/stage-out.txt"`,
	} {
		contains(want)
	}

	// SkipTLS defaults off → the s5cmd invocation carries no --no-verify-ssl.
	if strings.Contains(out, "--no-verify-ssl") {
		t.Errorf("did not expect --no-verify-ssl when SkipTLS is unset:\n%s", out)
	}
}

// TestGenerate_StagingSkipTLS verifies that the private-CA path (SkipTLS=true,
// set by the wiring layer when the MinIO endpoint is HTTPS) emits
// --no-verify-ssl as an s5cmd global flag — before the `run` subcommand — so
// the stage tasks can reach the private-CA MinIO (spec cluster-seam detail §1,
// mirrors the pipeline path's useTLS=false).
func TestGenerate_StagingSkipTLS(t *testing.T) {
	spec := Spec{
		Name: "penguins-rf-tls", Driver: "exec", Nodes: 1, Priority: 50,
		Staging: StagingSpec{
			Enabled:          true,
			StageInManifest:  "cp s3://b/common/penguins.csv analysis/data/external/penguins.csv\n",
			StageOutManifest: "cp analysis/data/06_models/rf.pkl s3://b/user/s/p/jobs/r1/outputs/analysis/data/06_models/rf.pkl\n",
			DestRoot:         "$NOMAD_ALLOC_DIR/data/r1",
			S5cmdPath:        "/nxf-work/bin/s5cmd",
			HostVolumeName:   "nf-work",
			HostVolumeSource: "/opt/abc-seedling/nf-work",
			HostVolumeMount:  "/nxf-work",
			SkipTLS:          true,
		},
	}
	out := Generate(spec, "run.sh", "python rf_train.py")
	// The global flag must precede the `run` subcommand. Both stage tasks carry it.
	if !strings.Contains(out, "--no-verify-ssl run") {
		t.Errorf("expected `--no-verify-ssl run` (global flag before subcommand) when SkipTLS=true:\n%s", out)
	}
	if strings.Count(out, "--no-verify-ssl") < 2 {
		t.Errorf("expected --no-verify-ssl on BOTH stage tasks (stage-in + stage-out):\n%s", out)
	}
}
