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
		`/nxf-work/bin/s5cmd `, // s5cmd invoked inside the sh wrapper
		// The endpoint is passed explicitly, not left to the environment:
		// s5cmd reads S3_ENDPOINT_URL and ignores AWS_ENDPOINT_URL, so an
		// env-only contract is one variable name away from addressing real
		// AWS S3 and failing as InvalidAccessKeyId.
		`--endpoint-url https://s3.seedling run`,
		`$NOMAD_ALLOC_DIR/data/r1`, // cd into the alloc-shared DestRoot
		// B12: the manifest lands at local/<file> = $NOMAD_TASK_DIR/<file>; the run
		// path must NOT double the "local/" (was /local/local/stage-in.txt → 404).
		`$NOMAD_TASK_DIR/stage-in.txt`,
		`$NOMAD_TASK_DIR/stage-out.txt`,
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

	// B12: the corrected run path must never double "local/".
	if strings.Contains(out, "$NOMAD_TASK_DIR/local/") {
		t.Errorf("doubled local/ path leaked into stage command:\n%s", out)
	}
}

// TestGenerate_StagingEmptyStageIn: a stage-in with no declared inputs (manifest
// is just a header comment — the --out-without---in case) must NOT run s5cmd; it
// only ensures the staged mirror exists, while stage-out still transfers.
// Regression guard for B12 (an empty manifest aborted the job).
func TestGenerate_StagingEmptyStageIn(t *testing.T) {
	spec := Spec{
		Name: "out-only", Driver: "exec", Nodes: 1, Priority: 50,
		Staging: StagingSpec{
			Enabled:          true,
			StageInManifest:  "# abc job-staging stage-in manifest — executed by `s5cmd run`\n",
			StageOutManifest: "cp report.txt s3://b/user/s/p/jobs/r1/outputs/report.txt\n",
			DestRoot:         "$NOMAD_ALLOC_DIR/data/r1",
			S5cmdPath:        "/nxf-work/bin/s5cmd",
			HostVolumeName:   "nf-work",
			HostVolumeMount:  "/nxf-work",
		},
	}
	out := Generate(spec, "run.sh", "echo hi")

	// Empty stage-in writes no manifest template and runs no s5cmd (mkdir-only).
	if strings.Contains(out, `destination = "local/stage-in.txt"`) {
		t.Errorf("empty stage-in should not write a manifest template:\n%s", out)
	}
	if n := strings.Count(out, "s5cmd run"); n != 1 {
		t.Errorf("expected exactly 1 s5cmd run (stage-out only), got %d:\n%s", n, out)
	}
	// stage-out still transfers, at the corrected (un-doubled) path.
	norm := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(norm, `$NOMAD_TASK_DIR/stage-out.txt`) {
		t.Errorf("stage-out should run s5cmd on $NOMAD_TASK_DIR/stage-out.txt:\n%s", out)
	}
	if strings.Contains(out, "$NOMAD_TASK_DIR/local/") {
		t.Errorf("doubled local/ path must not appear:\n%s", out)
	}
}

func TestManifestHasCommands(t *testing.T) {
	if manifestHasCommands("# header only\n") {
		t.Error("header-only manifest should report no commands")
	}
	if manifestHasCommands("   \n\n#comment\n") {
		t.Error("blank+comment manifest should report no commands")
	}
	if !manifestHasCommands("# header\ncp a b\n") {
		t.Error("manifest with a cp line should report commands")
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
