package job_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
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
		`type     = "batch"`,
		`count = 1`,
		`cores  = 4`,
		`memory = 8192`,
		`command  = "/bin/bash"`,
		`args     = ["local/job.sh"]`,
		`template {`,
		`data = <<-ABC_SCRIPT`,
		`#!/bin/bash`,
		"\nABC_SCRIPT\n",
		`destination = "local/job.sh"`,
		`perms       = "0755"`,
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

// ── Happy-path: #ABC scheduler directives ────────────────────────────────────

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
	checks := []string{`job "abc-serial"`, `count = 2`, `cores  = 8`, `memory = 16384`}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

func TestJobRun_RegionAndDCScheduler(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=regional-job
#ABC --region=za-cpt
#ABC --dc=za-cpt-dc1
#ABC --dc=za-cpt-dc2
echo hello
`
	p := writeTempScript(t, "regional.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `region   = "za-cpt"`) {
		t.Errorf("expected region in HCL, got:\n%s", out)
	}
	if !strings.Contains(out, `"za-cpt-dc1"`) || !strings.Contains(out, `"za-cpt-dc2"`) {
		t.Errorf("expected both datacenters in HCL, got:\n%s", out)
	}
}

func TestJobRun_Priority(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=hipri\n#ABC --priority=80\necho hi\n"
	p := writeTempScript(t, "hipri.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "priority = 80") {
		t.Errorf("expected priority=80, got:\n%s", out)
	}
}

func TestJobRun_DefaultPriority50(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=default-pri\necho hi\n"
	p := writeTempScript(t, "pri.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "priority = 50") {
		t.Errorf("expected default priority=50, got:\n%s", out)
	}
}

// ── Meta directives ──────────────────────────────────────────────────────────

func TestJobRun_MetaDirective(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=meta-job
#ABC --meta=sample_id=ZA-INST-2024-001
#ABC --meta=batch=48
#ABC --meta=pipeline_run=run-a1b2c3
echo hi
`
	p := writeTempScript(t, "meta.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checks := []string{
		`meta {`,
		`batch = "48"`,
		`pipeline_run = "run-a1b2c3"`,
		`sample_id = "ZA-INST-2024-001"`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in meta block, got:\n%s", want, out)
		}
	}
}

// ── Port (network) directives ────────────────────────────────────────────────

func TestJobRun_PortDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=mpi-job\n#ABC --port=mpi\necho hi\n"
	p := writeTempScript(t, "mpi.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `network {`) {
		t.Errorf("expected network block, got:\n%s", out)
	}
	if !strings.Contains(out, `port "mpi" {}`) {
		t.Errorf("expected port directive, got:\n%s", out)
	}
	if !strings.Contains(out, "NOMAD_IP_MPI") {
		t.Errorf("expected NOMAD_IP_MPI in env, got:\n%s", out)
	}
	if !strings.Contains(out, "NOMAD_PORT_MPI") {
		t.Errorf("expected NOMAD_PORT_MPI in env, got:\n%s", out)
	}
	if !strings.Contains(out, "NOMAD_ADDR_MPI") {
		t.Errorf("expected NOMAD_ADDR_MPI in env, got:\n%s", out)
	}
}

// ── Runtime-exposure boolean flags ───────────────────────────────────────────

func TestJobRun_RuntimeExposureFlags(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=expose-test
#ABC --alloc_id
#ABC --short_alloc_id
#ABC --alloc_name
#ABC --alloc_index
#ABC --job_id
#ABC --job_name
#ABC --parent_job_id
#ABC --group_name
#ABC --task_name
#ABC --cpu_limit
#ABC --cpu_cores
#ABC --mem_limit
#ABC --mem_max_limit
#ABC --alloc_dir
#ABC --task_dir
#ABC --secrets_dir
echo hi
`
	p := writeTempScript(t, "expose.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All these should appear in the "explicitly requested" section.
	exposed := []string{
		"NOMAD_ALLOC_ID",
		"NOMAD_SHORT_ALLOC_ID",
		"NOMAD_ALLOC_NAME",
		"NOMAD_ALLOC_INDEX",
		"NOMAD_JOB_ID",
		"NOMAD_JOB_NAME",
		"NOMAD_JOB_PARENT_ID",
		"NOMAD_GROUP_NAME",
		"NOMAD_TASK_NAME",
		"NOMAD_CPU_LIMIT",
		"NOMAD_CPU_CORES",
		"NOMAD_MEMORY_LIMIT",
		"NOMAD_MEMORY_MAX_LIMIT",
		"NOMAD_ALLOC_DIR",
		"NOMAD_TASK_DIR",
		"NOMAD_SECRETS_DIR",
	}
	// Find the "explicitly requested" section.
	section := out
	if idx := strings.Index(out, "Explicitly requested"); idx >= 0 {
		section = out[idx:]
	}
	for _, want := range exposed {
		if !strings.Contains(section, want) {
			t.Errorf("expected %q in runtime-exposure section, got:\n%s", want, section)
		}
	}
}

func TestJobRun_BareNamespaceFlagExposesEnv(t *testing.T) {
	// bare --namespace (no =value) → expose NOMAD_NAMESPACE runtime var
	script := "#!/bin/bash\n#ABC --name=ns-expose\n#ABC --namespace\necho hi\n"
	p := writeTempScript(t, "ns.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "NOMAD_NAMESPACE") {
		t.Errorf("expected NOMAD_NAMESPACE in env, got:\n%s", out)
	}
	// Must NOT set namespace = in the job block
	if strings.Contains(out, `namespace = "`) {
		t.Errorf("bare --namespace should not set scheduler namespace, got:\n%s", out)
	}
}

func TestJobRun_NamespaceWithValueSetsScheduler(t *testing.T) {
	// --namespace=hpc → scheduler placement (appears in job block)
	script := "#!/bin/bash\n#ABC --name=ns-sched\n#ABC --namespace=hpc\necho hi\n"
	p := writeTempScript(t, "ns2.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `namespace = "hpc"`) {
		t.Errorf("expected namespace=hpc in HCL job block, got:\n%s", out)
	}
}

func TestJobRun_BareDCFlagExposesEnv(t *testing.T) {
	// bare --dc → expose NOMAD_DC runtime var
	script := "#!/bin/bash\n#ABC --name=dc-expose\n#ABC --dc\necho hi\n"
	p := writeTempScript(t, "dc.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "NOMAD_DC") {
		t.Errorf("expected NOMAD_DC in env, got:\n%s", out)
	}
}

func TestJobRun_DCWithValueSetsScheduler(t *testing.T) {
	// --dc=za-cpt-dc1 → scheduler placement (datacenters = [])
	script := "#!/bin/bash\n#ABC --name=dc-sched\n#ABC --dc=za-cpt-dc1\necho hi\n"
	p := writeTempScript(t, "dc2.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"za-cpt-dc1"`) {
		t.Errorf("expected za-cpt-dc1 in datacenters, got:\n%s", out)
	}
}

// ── Priority: #ABC overrides #NOMAD ─────────────────────────────────────────

func TestJobRun_ABCOverridesNOMAD(t *testing.T) {
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
	if strings.Contains(out, `job "nomad-name"`) {
		t.Errorf("expected #NOMAD name to be overridden by #ABC, got:\n%s", out)
	}
}

// ── NOMAD env var fallback ────────────────────────────────────────────────────

func TestJobRun_NomadEnvVarFallback_Name(t *testing.T) {
	t.Setenv("NOMAD_JOB_NAME", "env-job-name")
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

// ── HPC compat env vars always present ───────────────────────────────────────

func TestJobRun_HPCCompatVarsAlwaysPresent(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=hpc-test\necho hi\n"
	p := writeTempScript(t, "hpc.sh", script)
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
	}
	for _, key := range keys {
		if !strings.Contains(out, key) {
			t.Errorf("expected HPC compat key %q in output\ngot:\n%s", key, out)
		}
	}
}

// ── Memory parsing ───────────────────────────────────────────────────────────

func TestJobRun_MemGigabytes(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=j\n#ABC --mem=2G\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 2048") {
		t.Errorf("expected memory=2048, got:\n%s", out)
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
	coresDirective := regexp.MustCompile(`(?m)^\s*cores\s*=`)
	if coresDirective.MatchString(out) {
		t.Errorf("directive after body line should be ignored, got:\n%s", out)
	}
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

func TestJobRun_MetaRequiresKeyEqualsValue(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=j\n#ABC --meta=justkey\necho hi\n"
	p := writeTempScript(t, "j.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for --meta without =value")
	}
}

func TestJobRun_MalformedNomadEnvVarIgnored(t *testing.T) {
	t.Setenv("NOMAD_CPU_CORES", "not-a-number")
	script := "#!/bin/bash\n#ABC --name=robust\necho hi\n"
	p := writeTempScript(t, "robust.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "resources {") {
		t.Errorf("expected no resources block when env var is malformed, got:\n%s", out)
	}
}
