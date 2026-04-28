package job_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/job"
	"github.com/spf13/cobra"
)

// isolatedABCConfigYAML is a minimal config so job HCL generation tests do not
// pick up the developer's real ~/.abc (which may inject abc-nodes namespaces).
const isolatedABCConfigYAML = `version: 1.0
active_context: isolated
contexts:
  isolated:
    cluster_type: abc-cloud
`

// executeCmdWithABCYAML runs "abc job run <args...>" with ABC_CONFIG_FILE
// pointing at a temp file containing yaml (overrides any inherited env).
func executeCmdWithABCYAML(t *testing.T, yaml string, args ...string) (string, error) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "abc.config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp abc config: %v", err)
	}
	t.Setenv("ABC_CONFIG_FILE", cfgPath)

	buf := &bytes.Buffer{}
	root := &cobra.Command{Use: "abc"}
	root.AddCommand(job.NewCmd())
	root.SetOut(buf)
	root.SetErr(buf)
	runArgs := append([]string(nil), args...)
	if !slices.Contains(runArgs, "--submit") && !slices.Contains(runArgs, "--dry-run") {
		runArgs = append(runArgs, "--no-submit")
	}
	root.SetArgs(append([]string{"job", "run"}, runArgs...))
	_, err := root.ExecuteC()
	return buf.String(), err
}

// executeCmdWithConfigPath runs job run with ABC_CONFIG_FILE set to an
// existing (or intentionally missing) path.
func executeCmdWithConfigPath(t *testing.T, cfgPath string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("ABC_CONFIG_FILE", cfgPath)

	buf := &bytes.Buffer{}
	root := &cobra.Command{Use: "abc"}
	root.AddCommand(job.NewCmd())
	root.SetOut(buf)
	root.SetErr(buf)
	runArgs := append([]string(nil), args...)
	if !slices.Contains(runArgs, "--submit") && !slices.Contains(runArgs, "--dry-run") {
		runArgs = append(runArgs, "--no-submit")
	}
	root.SetArgs(append([]string{"job", "run"}, runArgs...))
	_, err := root.ExecuteC()
	return buf.String(), err
}

// executeCmd runs "abc job run <args...>" with an isolated abc-cloud config.
func executeCmd(t *testing.T, args ...string) (string, error) {
	return executeCmdWithABCYAML(t, isolatedABCConfigYAML, args...)
}

func assertJobNamePrefix(t *testing.T, out, namePrefix string) {
	t.Helper()
	if !strings.Contains(out, fmt.Sprintf("job \"script-job-%s-", namePrefix)) {
		t.Fatalf("expected job name prefix %q in output, got:\n%s", "script-job-"+namePrefix, out)
	}
}

func TestJobRun_SlurmPreambleAutoModeUsesSlurmDriver(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name=legacy-slurm
#SBATCH --cpus-per-task=4
#SBATCH --mem=8G
#SBATCH --time=01:00:00
#SBATCH --partition=compute
echo hello
`
	p := writeTempScript(t, "legacy.slurm.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJobNamePrefix(t, out, "legacy-slurm")
	if !strings.Contains(out, `driver = "slurm"`) {
		t.Fatalf("expected slurm driver in output, got:\n%s", out)
	}
	if !strings.Contains(out, `cores  = 4`) {
		t.Fatalf("expected cores=4 in output, got:\n%s", out)
	}
	if !strings.Contains(out, `memory = 8192`) {
		t.Fatalf("expected memory=8192 in output, got:\n%s", out)
	}
	if !regexp.MustCompile(`queue\s*=\s*"compute"`).MatchString(out) {
		t.Fatalf("expected slurm partition mapped to queue, got:\n%s", out)
	}
	if strings.Contains(out, `command = "timeout"`) {
		t.Fatalf("expected slurm walltime to be configured without timeout wrapper, got:\n%s", out)
	}
	if !regexp.MustCompile(`args\s*=\s*\["-c",`).MatchString(out) {
		t.Fatalf("expected slurm task to execute inline script content via bash -c, got:\n%s", out)
	}
	if strings.Contains(out, `local/legacy.slurm.sh`) {
		t.Fatalf("did not expect slurm task to reference local script path, got:\n%s", out)
	}
	if strings.Contains(out, `template {`) {
		t.Fatalf("did not expect slurm driver path to emit a local template block, got:\n%s", out)
	}
}

func TestJobRun_SlurmPreambleMapsOutputErrorAndChdir(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name=legacy-io
#SBATCH --output=/shared/results/slurm.out
#SBATCH --error=/shared/results/slurm.err
#SBATCH --chdir=/shared/work
echo hello
`
	p := writeTempScript(t, "legacy-io.slurm.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`stdout_file\s*=\s*"/shared/results/slurm\.out"`).MatchString(out) {
		t.Fatalf("expected stdout_file mapping, got:\n%s", out)
	}
	if !regexp.MustCompile(`stderr_file\s*=\s*"/shared/results/slurm\.err"`).MatchString(out) {
		t.Fatalf("expected stderr_file mapping, got:\n%s", out)
	}
	if !regexp.MustCompile(`work_dir\s*=\s*"/shared/work"`).MatchString(out) {
		t.Fatalf("expected work_dir mapping, got:\n%s", out)
	}
}

func TestJobRun_SlurmInlineScriptEscapesNomadInterpolation(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name=legacy-escape
echo "${SLURM_JOB_ID:-unknown}"
`
	p := writeTempScript(t, "legacy-escape.slurm.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`args\s*=\s*\["-c",`).MatchString(out) {
		t.Fatalf("expected slurm inline script in args, got:\n%s", out)
	}
	if !strings.Contains(out, `$${SLURM_JOB_ID:-unknown}`) {
		t.Fatalf("expected Nomad interpolation to be escaped in slurm inline script, got:\n%s", out)
	}
}

func TestJobRun_HybridPreambleABCOverridesSlurmResources(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name=hybrid-job
#SBATCH --cpus-per-task=2
#ABC --cores=6
echo hello
`
	p := writeTempScript(t, "hybrid.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJobNamePrefix(t, out, "hybrid-job")
	if !strings.Contains(out, `driver = "slurm"`) {
		t.Fatalf("expected hybrid auto mode to default to slurm driver, got:\n%s", out)
	}
	if !regexp.MustCompile(`cores\s*=\s*6`).MatchString(out) {
		t.Fatalf("expected #ABC cores override, got:\n%s", out)
	}
	if !regexp.MustCompile(`cpus_per_task\s*=\s*6`).MatchString(out) {
		t.Fatalf("expected slurm cpus_per_task to follow overridden cores, got:\n%s", out)
	}
}

func TestJobRun_HybridAllowsABCDriverOverride(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name=hybrid-driver
#SBATCH --partition=compute
#ABC --driver=exec
echo hello
`
	p := writeTempScript(t, "hybrid-driver.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJobNamePrefix(t, out, "hybrid-driver")
	if !strings.Contains(out, `driver = "exec"`) {
		t.Fatalf("expected #ABC driver override to exec, got:\n%s", out)
	}
	if regexp.MustCompile(`queue\s*=\s*"compute"`).MatchString(out) {
		t.Fatalf("did not expect slurm-only queue config when driver is exec, got:\n%s", out)
	}
}

func TestJobRun_PreambleModeABCIgnoresSlurmDirectives(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name=slurm-name
echo hello
`
	p := writeTempScript(t, "abc-mode.sh", script)
	out, err := executeCmd(t, p, "--preamble-mode", "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJobNamePrefix(t, out, "abc-mode")
	if !strings.Contains(out, `driver = "exec"`) {
		t.Fatalf("expected abc preamble mode to keep default exec driver, got:\n%s", out)
	}
}

func TestJobRun_PreambleModeSlurmRequiresSBATCH(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=abc-only
echo hello
`
	p := writeTempScript(t, "abc-only.sh", script)
	_, err := executeCmd(t, p, "--preamble-mode", "slurm")
	if err == nil {
		t.Fatal("expected error when slurm preamble mode is selected without #SBATCH directives")
	}
	if !strings.Contains(err.Error(), "requires at least one #SBATCH directive") {
		t.Fatalf("unexpected error: %v", err)
	}
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
	assertJobNamePrefix(t, out, "serial-python")
	checks := []string{
		`type     = "batch"`,
		`count = 1`,
		`cores  = 4`,
		`memory = 8192`,
		`command = "/bin/bash"`,
		`args    = ["local/job.sh"]`,
		`template {`,
		`data        = <<-EOT`,
		`#!/bin/bash`,
		`destination = "local/job.sh"`,
		`perms       = "0755"`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SLURM_JOB_ID") || strings.Contains(out, "PBS_JOBID") {
		t.Errorf("did not expect HPC compatibility env aliases by default, got:\n%s", out)
	}
}

func TestJobRun_DriverConfigDirective(t *testing.T) {
	script := `#!/bin/bash
#ABC --driver=docker
#ABC --driver.config.image="docker.io/library/nginx:1.27-alpine"
#ABC --driver.config.volumes=["local/index.html:/usr/share/nginx/html/index.html"]
#ABC --driver.config.dns_servers=["1.1.1.1","8.8.8.8"]
exit 0
`
	p := writeTempScript(t, "driver_config.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "docker"`) {
		t.Fatalf("expected docker driver, got:\n%s", out)
	}
	if !strings.Contains(out, `"docker.io/library/nginx:1.27-alpine"`) {
		t.Fatalf("expected driver.image directive in output, got:\n%s", out)
	}
	// JSON-array driver.config values render as real HCL lists, not stringified JSON.
	if !strings.Contains(out, `["local/index.html:/usr/share/nginx/html/index.html"]`) {
		t.Fatalf("expected driver.volumes as HCL list, got:\n%s", out)
	}
	if !strings.Contains(out, `["1.1.1.1", "8.8.8.8"]`) {
		t.Fatalf("expected driver.dns_servers as HCL list, got:\n%s", out)
	}
	// command and args must NOT be rendered from driver.config — those are
	// owned by the script-wrap path. The script-wrap-set command should win.
	if !strings.Contains(out, `"/bin/bash"`) {
		t.Fatalf("expected script-wrap command='/bin/bash', got:\n%s", out)
	}
}

func TestJobRun_DriverConfigArgsRejected(t *testing.T) {
	// --driver.config.args would shadow the submitted script — must error at
	// parse time, not silently override.
	script := `#!/bin/bash
#ABC --driver=docker
#ABC --driver.config.image=alpine:3.19
#ABC --driver.config.args=["--cpu","1"]
exit 0
`
	p := writeTempScript(t, "driver_config_args.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatalf("expected error for --driver.config.args, got none")
	}
	if !strings.Contains(err.Error(), "--driver.config.args is not allowed") {
		t.Fatalf("expected explanatory error mentioning args, got: %v", err)
	}
}

func TestJobRun_DriverConfigCommandRejected(t *testing.T) {
	script := `#!/bin/bash
#ABC --driver=docker
#ABC --driver.config.image=alpine:3.19
#ABC --driver.config.command=stress-ng
exit 0
`
	p := writeTempScript(t, "driver_config_command.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatalf("expected error for --driver.config.command, got none")
	}
	if !strings.Contains(err.Error(), "--driver.config.command is not allowed") {
		t.Fatalf("expected explanatory error mentioning command, got: %v", err)
	}
}

func TestJobRun_ConstraintAffinityDirectives(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=affinity-test
#ABC --constraint=region==us-east
#ABC --affinity=datacenter==c1,weight=75
exit 0
`
	p := writeTempScript(t, "affinity.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "constraint {") {
		t.Fatalf("expected constraint block in output, got:\n%s", out)
	}
	if !strings.Contains(out, "attribute = \"region\"") {
		t.Fatalf("expected constraint attribute in output, got:\n%s", out)
	}
	if !strings.Contains(out, "operator  = \"==\"") {
		t.Fatalf("expected constraint operator in output, got:\n%s", out)
	}
	if !strings.Contains(out, "\"us-east\"") {
		t.Fatalf("expected constraint value in output, got:\n%s", out)
	}
	if !strings.Contains(out, "affinity {") {
		t.Fatalf("expected affinity block in output, got:\n%s", out)
	}
	if !strings.Contains(out, "attribute = \"datacenter\"") {
		t.Fatalf("expected affinity attribute in output, got:\n%s", out)
	}
	if !strings.Contains(out, "weight    = 75") {
		t.Fatalf("expected affinity weight in output, got:\n%s", out)
	}
}

func TestJobRun_ParamsAndCLIOverridePriority(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=preamble-name
#ABC --cores=2
sleep 1
`
	p := writeTempScript(t, "override.sh", script)
	paramsPath := filepath.Join(t.TempDir(), "params.yaml")
	if err := os.WriteFile(paramsPath, []byte("name: params-name\ncores: 4\n"), 0600); err != nil {
		t.Fatal(err)
	}

	out, err := executeCmd(t, p, "--params-file", paramsPath, "--name", "cli-name", "--cores", "6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJobNamePrefix(t, out, "cli-name")
	if !strings.Contains(out, `cores`) || !strings.Contains(out, `6`) {
		t.Errorf("expected cores=6, got:\n%s", out)
	}
}

func TestJobRun_OutputErrorPreamble(t *testing.T) {
	script := `#!/bin/bash
#ABC --output=job.out
#ABC --error=job.err
sleep 1
`
	p := writeTempScript(t, "outerr.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `meta`) || !strings.Contains(out, `abc_output`) || !strings.Contains(out, `abc_error`) {
		t.Fatalf("expected abc_output/abc_error metadata, got:\n%s", out)
	}
}

func TestJobRun_ReschedulePreamble(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=resched-job
#ABC --reschedule-mode=delay
#ABC --reschedule-attempts=3
#ABC --reschedule-interval=30s
#ABC --reschedule-delay=10s
#ABC --reschedule-max-delay=2m
sleep 1
`
	p := writeTempScript(t, "resched.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `abc_reschedule_mode`) {
		t.Fatalf("expected reschedule mode metadata, got:\n%s", out)
	}
	if !strings.Contains(out, `abc_submission_time`) {
		t.Fatalf("expected abc_submission_time metadata, got:\n%s", out)
	}
	if !strings.Contains(out, `abc_reschedule_attempts`) {
		t.Fatalf("expected reschedule attempts metadata, got:\n%s", out)
	}
	if !strings.Contains(out, `abc_reschedule_interval`) {
		t.Fatalf("expected reschedule interval metadata, got:\n%s", out)
	}
	if !strings.Contains(out, `abc_reschedule_delay`) {
		t.Fatalf("expected reschedule delay metadata, got:\n%s", out)
	}
	if !strings.Contains(out, `abc_reschedule_max_delay`) {
		t.Fatalf("expected reschedule max delay metadata, got:\n%s", out)
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
	assertJobNamePrefix(t, out, "ocean-model")
	checks := []string{
		`namespace = "hpc"`,
		`count = 4`,
		`cores`,
		`memory = 65536`,
		`7200`,
		`local/mpi_job.sh`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

func TestJobRun_HPCCompatVarsEnabledByDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=hpc-test\n#ABC --hpc_compat_env\necho hi\n"
	p := writeTempScript(t, "hpc_directive.sh", script)
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
			t.Errorf("expected HPC compat key %q in output when enabled\ngot:\n%s", key, out)
		}
	}
}

func TestJobRun_HPCCompatVarsEnabledByCLIFlag(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=hpc-test\necho hi\n"
	p := writeTempScript(t, "hpc_cli_flag.sh", script)
	out, err := executeCmd(t, p, "--hpc-compat-env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `SLURM_JOB_ID`) || !strings.Contains(out, `PBS_JOBID`) {
		t.Fatalf("expected HPC compat aliases when --hpc-compat-env is set, got:\n%s", out)
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
	assertJobNamePrefix(t, out, "abc-serial")
	checks := []string{`count = 2`, `cores  = 8`, `memory = 16384`}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, out)
		}
	}
}

func TestJobRun_ABCPreambleConda(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=conda-job
#ABC --conda=environment.yml
python -c "print(\"hello\")"
`
	p := writeTempScript(t, "conda.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `abc_conda`) || !strings.Contains(out, `environment.yml`) {
		t.Fatalf("expected abc_conda metadata in output, got:\n%s", out)
	}
}

func TestJobRun_CLIConda(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=conda-cli-job
python -c "print(\"hello\")"
`
	p := writeTempScript(t, "conda-cli.sh", script)
	out, err := executeCmd(t, p, "--conda", "env.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `abc_conda`) || !strings.Contains(out, `env.yaml`) {
		t.Fatalf("expected abc_conda metadata in output, got:\n%s", out)
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
	if !strings.Contains(out, `region`) || !strings.Contains(out, `za-cpt`) {
		t.Errorf("expected region in HCL, got:\n%s", out)
	}
	if !strings.Contains(out, `za-cpt-dc1`) || !strings.Contains(out, `za-cpt-dc2`) {
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
		`batch`,
		`pipeline_run`,
		`sample_id`,
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
	if !strings.Contains(out, `port "mpi"`) {
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

func TestJobRun_NoNetworkEnforced(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=no-network-job\n#ABC --no-network\necho hi\n"
	p := writeTempScript(t, "no-network.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "network {") {
		t.Fatalf("expected network block, got:\n%s", out)
	}
	if !strings.Contains(out, "mode = \"none\"") {
		t.Fatalf("expected network mode=none, got:\n%s", out)
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
	assertJobNamePrefix(t, out, "abc-name")
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
	assertJobNamePrefix(t, out, "env-job-name")
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

// ── HPC compat env vars are opt-in ───────────────────────────────────────────

func TestJobRun_HPCCompatVarsDisabledByDefault(t *testing.T) {
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
		if strings.Contains(out, key) {
			t.Errorf("did not expect HPC compat key %q in output by default\ngot:\n%s", key, out)
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
	assertJobNamePrefix(t, out, "my-analysis")
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
	if !strings.Contains(out, `command`) || !strings.Contains(out, `timeout`) {
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
	assertJobNamePrefix(t, out, "boundary-test")
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
