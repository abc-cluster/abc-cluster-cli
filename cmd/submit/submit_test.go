package submit_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/submit"
	"github.com/spf13/cobra"
)

// executeCmd runs the submit command with the given args and returns stdout.
func executeCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	cmd := submit.NewCmd()
	// Wrap in a root command so persistent-flag parsing works correctly.
	root := &cobra.Command{Use: "abc"}
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"submit"}, args...))
	_, err := root.ExecuteC()
	return buf.String(), err
}

// writeTempScript writes content to a temp file and returns its path.
func writeTempScript(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// ── Happy-path tests ────────────────────────────────────────────────────────

func TestSubmit_SerialPythonJob(t *testing.T) {
	script := `#!/bin/bash
#NOMAD --name=serial-python
#NOMAD --nodes=1
#NOMAD --cores=4
#NOMAD --mem=8G
python analysis.py
`
	p := writeTempScript(t, "job.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		`job "serial-python"`,
		`type = "batch"`,
		`count = 1`,
		`cores  = 4`,
		`memory = 8192`,
		`command  = "/bin/bash"`,
		`args     = ["local/job.sh"]`,
		`source = "job.sh"`,
		`SLURM_JOB_ID`,
		`PBS_JOBID`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

func TestSubmit_MultiNodeMPIJob(t *testing.T) {
	script := `#!/bin/bash
#NOMAD --name=ocean-model
#NOMAD --namespace=hpc
#NOMAD --nodes=4
#NOMAD --cores=28
#NOMAD --mem=64G
#NOMAD --time=02:00:00
mpirun -np 112 ./ocean_model
`
	p := writeTempScript(t, "mpi_job.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		`job "ocean-model"`,
		`namespace = "hpc"`,
		`count = 4`,
		`cores  = 28`,
		`memory = 65536`,
		`command  = "timeout"`,
		// 2 hours = 7200 seconds
		`args     = ["7200", "/bin/bash", "local/mpi_job.sh"]`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

func TestSubmit_GPUDirective(t *testing.T) {
	script := `#!/bin/bash
#NOMAD --name=gpu-train
#NOMAD --gpus=2
python train.py
`
	p := writeTempScript(t, "gpu.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		`device "nvidia/gpu"`,
		`count = 2`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

func TestSubmit_ChdirDirective(t *testing.T) {
	script := `#!/bin/bash
#NOMAD --name=chdir-job
#NOMAD --chdir=/scratch/user/project
./run.sh
`
	p := writeTempScript(t, "chdir.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, `work_dir = "/scratch/user/project"`) {
		t.Errorf("expected work_dir in output\ngot:\n%s", out)
	}
}

func TestSubmit_DependDirective(t *testing.T) {
	script := `#!/bin/bash
#NOMAD --name=dep-job
#NOMAD --depend=after:job-abc123
./step2.sh
`
	p := writeTempScript(t, "dep.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		`task "wait-dependency"`,
		`hook    = "prestart"`,
		`sidecar = false`,
		`after:job-abc123`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

// ── Memory parsing tests ────────────────────────────────────────────────────

func TestSubmit_MemGigabytes(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --name=j\n#NOMAD --mem=16G\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 16384") {
		t.Errorf("expected memory=16384, got:\n%s", out)
	}
}

func TestSubmit_MemMegabytes(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --name=j\n#NOMAD --mem=512M\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 512") {
		t.Errorf("expected memory=512, got:\n%s", out)
	}
}

func TestSubmit_MemKilobytes(t *testing.T) {
	// 4096 KiB = 4 MiB exactly
	script := "#!/bin/bash\n#NOMAD --name=j\n#NOMAD --mem=4096K\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 4") {
		t.Errorf("expected memory=4, got:\n%s", out)
	}
}

func TestSubmit_MemNoSuffix(t *testing.T) {
	// No suffix → treated as MiB
	script := "#!/bin/bash\n#NOMAD --name=j\n#NOMAD --mem=256\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 256") {
		t.Errorf("expected memory=256, got:\n%s", out)
	}
}

// ── Default-value tests ─────────────────────────────────────────────────────

func TestSubmit_DefaultNameFromFilename(t *testing.T) {
	// No --name directive → name derived from filename.
	script := "#!/bin/bash\necho hello\n"
	p := writeTempScript(t, "my-analysis.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `job "my-analysis"`) {
		t.Errorf("expected job name derived from filename, got:\n%s", out)
	}
}

func TestSubmit_DefaultNodes1(t *testing.T) {
	// No --nodes directive → count = 1.
	script := "#!/bin/bash\n#NOMAD --name=solo\necho hello\n"
	p := writeTempScript(t, "solo.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "count = 1") {
		t.Errorf("expected count=1 default, got:\n%s", out)
	}
}

func TestSubmit_NoNomadDirectives(t *testing.T) {
	// Script with no directives at all should still produce valid HCL.
	script := "#!/bin/bash\n# A plain comment\necho hello\n"
	p := writeTempScript(t, "plain.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `job "plain"`) {
		t.Errorf("expected job name 'plain', got:\n%s", out)
	}
	if !strings.Contains(out, "count = 1") {
		t.Errorf("expected count=1, got:\n%s", out)
	}
}

// ── Environment-variable mapping test ──────────────────────────────────────

func TestSubmit_EnvVarMappingAlwaysPresent(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --name=env-test\necho hi\n"
	p := writeTempScript(t, "env.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envMappings := []string{
		`SLURM_JOB_ID`,
		`PBS_JOBID`,
		`SLURM_JOB_NAME`,
		`PBS_JOBNAME`,
		`SLURM_SUBMIT_DIR`,
		`PBS_O_WORKDIR`,
		`SLURM_ARRAY_TASK_ID`,
		`PBS_ARRAYID`,
		`SLURM_NTASKS`,
		`PBS_NP`,
		`SLURMD_NODENAME`,
		`PBS_O_HOST`,
		`SLURM_CPUS_ON_NODE`,
		`PBS_NUM_PPN`,
		`SLURM_MEM_PER_NODE`,
		`PBS_MEM`,
		`NOMAD_ALLOC_ID`,
		`NOMAD_JOB_NAME`,
		`NOMAD_TASK_DIR`,
		`NOMAD_ALLOC_INDEX`,
		`NOMAD_GROUP_COUNT`,
		`NOMAD_ALLOC_HOST`,
		`NOMAD_CPU_CORES`,
		`NOMAD_MEMORY_LIMIT`,
	}
	for _, key := range envMappings {
		if !strings.Contains(out, key) {
			t.Errorf("expected env key %q in output\ngot:\n%s", key, out)
		}
	}
}

// ── Resources block omitted when nothing specified ──────────────────────────

func TestSubmit_ResourcesBlockOmittedWhenEmpty(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --name=no-res\necho hi\n"
	p := writeTempScript(t, "no_res.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "resources {") {
		t.Errorf("expected no resources block, got:\n%s", out)
	}
}

// ── Walltime test ───────────────────────────────────────────────────────────

func TestSubmit_WalltimeConvertedToSeconds(t *testing.T) {
	// 01:30:00 = 5400 seconds
	script := "#!/bin/bash\n#NOMAD --name=timed\n#NOMAD --time=01:30:00\n./run.sh\n"
	p := writeTempScript(t, "timed.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"5400"`) {
		t.Errorf("expected 5400 seconds in timeout args, got:\n%s", out)
	}
	if !strings.Contains(out, `command  = "timeout"`) {
		t.Errorf("expected timeout command, got:\n%s", out)
	}
}

// ── Error-case tests ────────────────────────────────────────────────────────

func TestSubmit_MissingScriptArg(t *testing.T) {
	_, err := executeCmd(t)
	if err == nil {
		t.Fatal("expected error for missing script argument")
	}
}

func TestSubmit_ScriptNotFound(t *testing.T) {
	_, err := executeCmd(t, "/nonexistent/script.sh")
	if err == nil {
		t.Fatal("expected error for nonexistent script")
	}
	if !strings.Contains(err.Error(), "cannot open script") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSubmit_InvalidDirectiveFormat(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --name=ok\n#NOMAD badformat\necho hi\n"
	p := writeTempScript(t, "bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for invalid directive format")
	}
}

func TestSubmit_UnknownDirective(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --unknown=value\necho hi\n"
	p := writeTempScript(t, "unk.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for unknown directive")
	}
	if !strings.Contains(err.Error(), "unknown #NOMAD directive") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSubmit_InvalidNodes(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --nodes=abc\necho hi\n"
	p := writeTempScript(t, "bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for non-integer nodes")
	}
}

func TestSubmit_InvalidMemory(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --mem=notanumber\necho hi\n"
	p := writeTempScript(t, "bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for invalid memory value")
	}
}

func TestSubmit_InvalidTime(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --time=1:30\necho hi\n"
	p := writeTempScript(t, "bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for invalid time format")
	}
	if !strings.Contains(err.Error(), "HH:MM:SS") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ── Preamble boundary test ──────────────────────────────────────────────────

func TestSubmit_DirectivesStopAtFirstNonComment(t *testing.T) {
	// The #NOMAD line after the non-comment body line must be ignored.
	script := `#!/bin/bash
#NOMAD --name=boundary-test
echo "body starts here"
#NOMAD --cores=99
`
	p := writeTempScript(t, "boundary.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The cores directive after the echo line must NOT appear in the output.
	if strings.Contains(out, "cores") {
		t.Errorf("directive after body line should be ignored, got:\n%s", out)
	}
}
