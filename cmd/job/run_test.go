package job_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/job"
	"github.com/spf13/cobra"
)

// executeCmd runs "abc job run <args...>" and returns stdout.
func executeCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	root := &cobra.Command{Use: "abc"}
	root.AddCommand(job.NewCmd())
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"job", "run"}, args...))
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

// ── Happy-path: #NOMAD preamble ─────────────────────────────────────────────

func TestJobRun_SerialPythonJob(t *testing.T) {
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

func TestJobRun_MultiNodeMPIJob(t *testing.T) {
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
		`args     = ["7200", "/bin/bash", "local/mpi_job.sh"]`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

// ── Happy-path: #ABC preamble ────────────────────────────────────────────────

func TestJobRun_ABCPreambleBasic(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=abc-serial
#ABC --nodes=2
#ABC --cores=8
#ABC --mem=16G
python train.py
`
	p := writeTempScript(t, "abc_job.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		`job "abc-serial"`,
		`count = 2`,
		`cores  = 8`,
		`memory = 16384`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

// ── Priority: #ABC overrides #NOMAD ─────────────────────────────────────────

func TestJobRun_ABCOverridesNOMAD(t *testing.T) {
	// Both markers present; #ABC values must win for every field.
	script := `#!/bin/bash
#NOMAD --name=nomad-name
#NOMAD --nodes=2
#NOMAD --cores=4
#NOMAD --mem=8G
#ABC --name=abc-name
#ABC --nodes=8
#ABC --cores=16
#ABC --mem=32G
echo hello
`
	p := writeTempScript(t, "mixed.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ABC values
	if !strings.Contains(out, `job "abc-name"`) {
		t.Errorf("expected #ABC name to win, got:\n%s", out)
	}
	if !strings.Contains(out, "count = 8") {
		t.Errorf("expected #ABC nodes=8, got:\n%s", out)
	}
	if !strings.Contains(out, "cores  = 16") {
		t.Errorf("expected #ABC cores=16, got:\n%s", out)
	}
	if !strings.Contains(out, "memory = 32768") {
		t.Errorf("expected #ABC mem=32G=32768 MiB, got:\n%s", out)
	}

	// NOMAD values must NOT appear
	if strings.Contains(out, `job "nomad-name"`) {
		t.Errorf("expected #NOMAD name to be overridden by #ABC, got:\n%s", out)
	}
}

func TestJobRun_ABCPartialOverridesNOMAD(t *testing.T) {
	// #ABC only sets name; #NOMAD sets the resource fields.
	script := `#!/bin/bash
#NOMAD --name=nomad-name
#NOMAD --cores=4
#NOMAD --mem=8G
#ABC --name=abc-name
echo hello
`
	p := writeTempScript(t, "partial.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, `job "abc-name"`) {
		t.Errorf("expected #ABC name to win, got:\n%s", out)
	}
	// #NOMAD resource fields still present
	if !strings.Contains(out, "cores  = 4") {
		t.Errorf("expected #NOMAD cores to remain, got:\n%s", out)
	}
	if !strings.Contains(out, "memory = 8192") {
		t.Errorf("expected #NOMAD memory to remain, got:\n%s", out)
	}
}

// ── Priority: NOMAD env vars as fallback ────────────────────────────────────

func TestJobRun_NomadEnvVarFallback_Name(t *testing.T) {
	t.Setenv("NOMAD_JOB_NAME", "env-job-name")
	// Script has no #ABC or #NOMAD --name directive.
	script := "#!/bin/bash\n#NOMAD --cores=4\necho hi\n"
	p := writeTempScript(t, "env_name.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `job "env-job-name"`) {
		t.Errorf("expected env var name to be used, got:\n%s", out)
	}
}

func TestJobRun_NomadEnvVarFallback_Resources(t *testing.T) {
	t.Setenv("NOMAD_GROUP_COUNT", "3")
	t.Setenv("NOMAD_CPU_CORES", "8")
	t.Setenv("NOMAD_MEMORY_LIMIT", "4096")
	script := "#!/bin/bash\n#NOMAD --name=env-res\necho hi\n"
	p := writeTempScript(t, "env_res.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "count = 3") {
		t.Errorf("expected NOMAD_GROUP_COUNT=3, got:\n%s", out)
	}
	if !strings.Contains(out, "cores  = 8") {
		t.Errorf("expected NOMAD_CPU_CORES=8, got:\n%s", out)
	}
	if !strings.Contains(out, "memory = 4096") {
		t.Errorf("expected NOMAD_MEMORY_LIMIT=4096, got:\n%s", out)
	}
}

func TestJobRun_NomadEnvVarFallback_Namespace(t *testing.T) {
	t.Setenv("NOMAD_NAMESPACE", "env-namespace")
	script := "#!/bin/bash\n#NOMAD --name=ns-job\necho hi\n"
	p := writeTempScript(t, "ns.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `namespace = "env-namespace"`) {
		t.Errorf("expected NOMAD_NAMESPACE from env, got:\n%s", out)
	}
}

func TestJobRun_NomadPreambleOverridesEnvVar(t *testing.T) {
	// #NOMAD preamble must override env var.
	t.Setenv("NOMAD_JOB_NAME", "env-name")
	t.Setenv("NOMAD_CPU_CORES", "2")
	script := "#!/bin/bash\n#NOMAD --name=preamble-name\n#NOMAD --cores=12\necho hi\n"
	p := writeTempScript(t, "prio.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `job "preamble-name"`) {
		t.Errorf("expected #NOMAD preamble name to win over env var, got:\n%s", out)
	}
	if !strings.Contains(out, "cores  = 12") {
		t.Errorf("expected #NOMAD preamble cores to win over env var, got:\n%s", out)
	}
}

func TestJobRun_ABCOverridesEnvVar(t *testing.T) {
	// #ABC must override env vars.
	t.Setenv("NOMAD_JOB_NAME", "env-name")
	t.Setenv("NOMAD_CPU_CORES", "2")
	script := "#!/bin/bash\n#ABC --name=abc-final\n#ABC --cores=24\necho hi\n"
	p := writeTempScript(t, "abc_prio.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `job "abc-final"`) {
		t.Errorf("expected #ABC name to win over env var, got:\n%s", out)
	}
	if !strings.Contains(out, "cores  = 24") {
		t.Errorf("expected #ABC cores to win over env var, got:\n%s", out)
	}
}

// ── NOMAD env directive ──────────────────────────────────────────────────────

func TestJobRun_NomadEnvDirectiveDefaultsToRuntimeValue(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --name=env-vars\n#NOMAD --env=NOMAD_ALLOC_ID\n#NOMAD --env=NOMAD_REGION\n#NOMAD --env=NOMAD_TASK_DIR\necho hi\n"
	p := writeTempScript(t, "env_vars.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checks := []string{
		`NOMAD_ALLOC_ID = "${NOMAD_ALLOC_ID}"`,
		`NOMAD_REGION = "${NOMAD_REGION}"`,
		`NOMAD_TASK_DIR = "${NOMAD_TASK_DIR}"`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

func TestJobRun_NomadEnvDirectiveExplicitValueABC(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=env-vars\n#ABC --env=NOMAD_REGION=global\necho hi\n"
	p := writeTempScript(t, "env_explicit.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `NOMAD_REGION = "global"`) {
		t.Errorf("expected explicit NOMAD_REGION value, got:\n%s", out)
	}
}

func TestJobRun_NomadEnvDirectiveExplicitValueFromNomad(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --name=env-vars\n#NOMAD --env=NOMAD_REGION=global\necho hi\n"
	p := writeTempScript(t, "env_explicit_nomad.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `NOMAD_REGION = "global"`) {
		t.Errorf("expected explicit NOMAD_REGION value, got:\n%s", out)
	}
}

func TestJobRun_NomadEnvDirectiveABCOverridesNomad(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --name=env-vars\n#NOMAD --env=NOMAD_REGION=global\n#ABC --env=NOMAD_REGION=local\necho hi\n"
	p := writeTempScript(t, "env_override.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `NOMAD_REGION = "local"`) {
		t.Errorf("expected #ABC env override, got:\n%s", out)
	}
	if strings.Contains(out, `NOMAD_REGION = "global"`) {
		t.Errorf("expected #NOMAD env to be overridden, got:\n%s", out)
	}
}

func TestJobRun_NomadEnvDirectiveRejectsNonNomad(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=env-vars\n#ABC --env=FOO=bar\necho hi\n"
	p := writeTempScript(t, "env_bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for non-NOMAD env var")
	}
	if !strings.Contains(err.Error(), "NOMAD_*") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ── GPU, chdir, depend directives ───────────────────────────────────────────

func TestJobRun_GPUDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=gpu-train\n#ABC --gpus=2\npython train.py\n"
	p := writeTempScript(t, "gpu.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checks := []string{`device "nvidia/gpu"`, `count = 2`}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

func TestJobRun_ChdirDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=chdir-job\n#ABC --chdir=/scratch/user/project\n./run.sh\n"
	p := writeTempScript(t, "chdir.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `work_dir = "/scratch/user/project"`) {
		t.Errorf("expected work_dir in output\ngot:\n%s", out)
	}
}

func TestJobRun_DependDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=dep-job\n#ABC --depend=after:job-abc123\n./step2.sh\n"
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

// ── Memory parsing ───────────────────────────────────────────────────────────

func TestJobRun_MemGigabytes(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=j\n#ABC --mem=16G\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 16384") {
		t.Errorf("expected memory=16384, got:\n%s", out)
	}
}

func TestJobRun_MemMegabytes(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=j\n#ABC --mem=512M\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 512") {
		t.Errorf("expected memory=512, got:\n%s", out)
	}
}

func TestJobRun_MemKilobytes(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=j\n#ABC --mem=4096K\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 4") {
		t.Errorf("expected memory=4 MiB, got:\n%s", out)
	}
}

func TestJobRun_MemNoSuffix(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=j\n#ABC --mem=256\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 256") {
		t.Errorf("expected memory=256, got:\n%s", out)
	}
}

// ── Defaults ─────────────────────────────────────────────────────────────────

func TestJobRun_DefaultNameFromFilename(t *testing.T) {
	script := "#!/bin/bash\necho hello\n"
	p := writeTempScript(t, "my-analysis.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `job "my-analysis"`) {
		t.Errorf("expected job name from filename, got:\n%s", out)
	}
}

func TestJobRun_DefaultNodes1(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=solo\necho hello\n"
	p := writeTempScript(t, "solo.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "count = 1") {
		t.Errorf("expected count=1 default, got:\n%s", out)
	}
}

func TestJobRun_NoDirectivesAtAll(t *testing.T) {
	script := "#!/bin/bash\n# plain comment\necho hello\n"
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

// ── Env-var mapping always present ──────────────────────────────────────────

func TestJobRun_EnvVarMappingAlwaysPresent(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=env-test\necho hi\n"
	p := writeTempScript(t, "env.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	keys := []string{
		`SLURM_JOB_ID`, `PBS_JOBID`,
		`SLURM_JOB_NAME`, `PBS_JOBNAME`,
		`SLURM_SUBMIT_DIR`, `PBS_O_WORKDIR`,
		`SLURM_ARRAY_TASK_ID`, `PBS_ARRAYID`,
		`SLURM_NTASKS`, `PBS_NP`,
		`SLURMD_NODENAME`, `PBS_O_HOST`,
		`SLURM_CPUS_ON_NODE`, `PBS_NUM_PPN`,
		`SLURM_MEM_PER_NODE`, `PBS_MEM`,
		`NOMAD_ALLOC_ID`, `NOMAD_JOB_NAME`,
		`NOMAD_TASK_DIR`, `NOMAD_ALLOC_INDEX`,
		`NOMAD_GROUP_COUNT`, `NOMAD_ALLOC_HOST`,
		`NOMAD_CPU_CORES`, `NOMAD_MEMORY_LIMIT`,
	}
	for _, key := range keys {
		if !strings.Contains(out, key) {
			t.Errorf("expected env key %q in output\ngot:\n%s", key, out)
		}
	}
}

// ── Resources block omitted when none specified ──────────────────────────────

func TestJobRun_ResourcesBlockOmittedWhenEmpty(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=no-res\necho hi\n"
	p := writeTempScript(t, "no_res.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "resources {") {
		t.Errorf("expected no resources block, got:\n%s", out)
	}
}

// ── Walltime ─────────────────────────────────────────────────────────────────

func TestJobRun_WalltimeConvertedToSeconds(t *testing.T) {
	// 01:30:00 = 5400 seconds
	script := "#!/bin/bash\n#ABC --name=timed\n#ABC --time=01:30:00\n./run.sh\n"
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

// ── Preamble boundary ────────────────────────────────────────────────────────

func TestJobRun_DirectivesStopAtFirstNonComment(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=boundary-test
echo "body starts here"
#ABC --cores=99
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
	// No resource directives were processed so the resources block must be absent.
	if strings.Contains(out, "resources {") {
		t.Errorf("expected no resources block when body-line directives are ignored, got:\n%s", out)
	}
	// Only the preamble name directive was processed.
	if !strings.Contains(out, `job "boundary-test"`) {
		t.Errorf("expected preamble name directive to be applied, got:\n%s", out)
	}
}

// ── Error cases ──────────────────────────────────────────────────────────────

func TestJobRun_MissingScriptArg(t *testing.T) {
	_, err := executeCmd(t)
	if err == nil {
		t.Fatal("expected error for missing script argument")
	}
}

func TestJobRun_ScriptNotFound(t *testing.T) {
	_, err := executeCmd(t, "/nonexistent/script.sh")
	if err == nil {
		t.Fatal("expected error for nonexistent script")
	}
	if !strings.Contains(err.Error(), "cannot open script") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestJobRun_InvalidABCDirectiveFormat(t *testing.T) {
	script := "#!/bin/bash\n#ABC badformat\necho hi\n"
	p := writeTempScript(t, "bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for invalid #ABC directive format")
	}
	if !strings.Contains(err.Error(), "#ABC") {
		t.Errorf("expected #ABC in error message, got: %v", err)
	}
}

func TestJobRun_InvalidNOMADDirectiveFormat(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --name=ok\n#NOMAD badformat\necho hi\n"
	p := writeTempScript(t, "bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for invalid #NOMAD directive format")
	}
	if !strings.Contains(err.Error(), "#NOMAD") {
		t.Errorf("expected #NOMAD in error message, got: %v", err)
	}
}

func TestJobRun_UnknownABCDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --unknown=value\necho hi\n"
	p := writeTempScript(t, "unk.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for unknown #ABC directive")
	}
	if !strings.Contains(err.Error(), "unknown #ABC directive") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestJobRun_UnknownNOMADDirective(t *testing.T) {
	script := "#!/bin/bash\n#NOMAD --unknown=value\necho hi\n"
	p := writeTempScript(t, "unk.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for unknown #NOMAD directive")
	}
	if !strings.Contains(err.Error(), "unknown #NOMAD directive") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestJobRun_InvalidNodes(t *testing.T) {
	script := "#!/bin/bash\n#ABC --nodes=abc\necho hi\n"
	p := writeTempScript(t, "bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for non-integer nodes")
	}
}

func TestJobRun_InvalidMemory(t *testing.T) {
	script := "#!/bin/bash\n#ABC --mem=notanumber\necho hi\n"
	p := writeTempScript(t, "bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for invalid memory value")
	}
}

func TestJobRun_InvalidTime(t *testing.T) {
	script := "#!/bin/bash\n#ABC --time=1:30\necho hi\n"
	p := writeTempScript(t, "bad.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for invalid time format")
	}
	if !strings.Contains(err.Error(), "HH:MM:SS") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestJobRun_MalformedNomadEnvVarIgnored(t *testing.T) {
	// Non-integer NOMAD_CPU_CORES should be silently ignored.
	t.Setenv("NOMAD_CPU_CORES", "not-a-number")
	script := "#!/bin/bash\n#ABC --name=robust\necho hi\n"
	p := writeTempScript(t, "robust.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error (malformed env var should be ignored): %v", err)
	}
	// No resources block because only malformed env var was provided.
	if strings.Contains(out, "resources {") {
		t.Errorf("expected no resources block when env var is malformed, got:\n%s", out)
	}
}
