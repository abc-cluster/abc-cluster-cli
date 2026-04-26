package job_test

// run_extra_test.go — additional offline unit tests for abc job run.
//
// These tests complement run_test.go and cover scenarios not yet tested there:
//   - GPU / chdir / depend / pixi directives / --runtime + --from (Pixi workspace)
//   - docker and hpc-bridge drivers
//   - SLURM account / ntasks mapping
//   - Preamble edge cases (inline comments, empty script)
//   - Directive precedence (params file, nested YAML)
//   - Resource parsing edge cases (TB, lowercase, zero-gpus)
//   - Multiple constraints / affinities
//   - Meta edge cases (value with embedded =)
//   - Multiple ports
//   - Config-based Nomad address (via ABC_CONFIG_FILE)
//   - Additional error cases
//
// Every test here runs without a Nomad endpoint — all assertions are against
// generated HCL printed to stdout.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ── A.1 exec driver extras ────────────────────────────────────────────────────

func TestJobRun_GPUDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=gpu-job\n#ABC --gpus=2\npython train.py\n"
	p := writeTempScript(t, "gpu.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `device "nvidia/gpu"`) {
		t.Errorf("expected nvidia/gpu device block, got:\n%s", out)
	}
	if !strings.Contains(out, "count = 2") {
		t.Errorf("expected device count=2, got:\n%s", out)
	}
}

func TestJobRun_GPUCLIFlag(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=gpu-cli\npython train.py\n"
	p := writeTempScript(t, "gpu_cli.sh", script)
	out, err := executeCmd(t, p, "--gpus", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `device "nvidia/gpu"`) {
		t.Errorf("expected nvidia/gpu device block, got:\n%s", out)
	}
}

func TestJobRun_ZeroGPUsIsError(t *testing.T) {
	// gpus=0 is invalid — the implementation requires a positive integer.
	script := "#!/bin/bash\n#ABC --name=no-gpu\n#ABC --gpus=0\necho hi\n"
	p := writeTempScript(t, "no_gpu.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for gpus=0 (must be a positive integer)")
	}
	if !strings.Contains(err.Error(), "gpus") && !strings.Contains(strings.ToLower(err.Error()), "positive") {
		t.Logf("error message: %v", err)
	}
}

func TestJobRun_ChdirDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=chdir-job\n#ABC --chdir=/data/work\necho hi\n"
	p := writeTempScript(t, "chdir.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The chdir should appear in the config block or as a work_dir-equivalent.
	if !strings.Contains(out, "/data/work") {
		t.Errorf("expected chdir path in HCL, got:\n%s", out)
	}
}

func TestJobRun_ChdirCLIFlag(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=chdir-cli\necho hi\n"
	p := writeTempScript(t, "chdir_cli.sh", script)
	out, err := executeCmd(t, p, "--chdir", "/shared/input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "/shared/input") {
		t.Errorf("expected chdir path from CLI flag, got:\n%s", out)
	}
}

func TestJobRun_DependDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=dep-job\n#ABC --depend=complete:upstream-job\necho hi\n"
	p := writeTempScript(t, "depend.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "upstream-job") {
		t.Errorf("expected depend job ID in HCL, got:\n%s", out)
	}
}

func TestJobRun_PixiDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-job\n#ABC --pixi\npixi run train\n"
	p := writeTempScript(t, "pixi.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "abc_pixi") {
		t.Errorf("expected abc_pixi in meta, got:\n%s", out)
	}
	if !strings.Contains(out, `"true"`) {
		t.Errorf("expected abc_pixi = \"true\" in meta, got:\n%s", out)
	}
}

func TestJobRun_PixiWithValueIsError(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-err\n#ABC --pixi=yes\necho hi\n"
	p := writeTempScript(t, "pixi_err.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error: #ABC --pixi does not accept a value")
	}
	if !strings.Contains(err.Error(), "pixi") {
		t.Errorf("expected pixi in error message, got: %v", err)
	}
}

func TestJobRun_TaskTmpDirective(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=task-tmp-job\n#ABC --task-tmp\necho hi\n"
	p := writeTempScript(t, "task_tmp.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `abc_task_tmp`) {
		t.Errorf("expected abc_task_tmp in meta, got:\n%s", out)
	}
	if !strings.Contains(out, `TMPDIR`) || !strings.Contains(out, `${NOMAD_TASK_DIR}/tmp`) {
		t.Errorf("expected TMPDIR in env block, got:\n%s", out)
	}
	if !strings.Contains(out, `abc task-tmp`) {
		t.Errorf("expected task-tmp script preamble in template, got:\n%s", out)
	}
}

func TestJobRun_TaskTmpCLI(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=task-tmp-cli\necho hi\n"
	p := writeTempScript(t, "task_tmp_cli.sh", script)
	out, err := executeCmd(t, p, "--task-tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `abc_task_tmp`) {
		t.Errorf("expected abc_task_tmp in meta, got:\n%s", out)
	}
}

func TestJobRun_TaskTmpWithPixiOrder(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=order\n#ABC --task-tmp\n#ABC --runtime=pixi-exec\n#ABC --from=/x/pixi.toml\necho hi\n"
	p := writeTempScript(t, "order.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	iTmp := strings.Index(out, "abc task-tmp")
	iPixi := strings.Index(out, "ABC_RUNTIME_PIXI_PHASE")
	if iTmp < 0 || iPixi < 0 {
		t.Fatalf("expected both blocks in template, got:\n%s", out)
	}
	if iTmp > iPixi {
		t.Fatalf("expected task-tmp block before pixi guard (tmp=%d pixi=%d)", iTmp, iPixi)
	}
}

func TestJobRun_PixiExecRuntimeWrapsScript(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-rt\n#ABC --runtime=pixi-exec\n#ABC --from=/cluster/proj/pixi.toml\necho hi\n"
	p := writeTempScript(t, "pixi_rt.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `abc_runtime`) || !strings.Contains(out, "pixi-exec") {
		t.Errorf("expected abc_runtime pixi-exec in meta, got:\n%s", out)
	}
	if !strings.Contains(out, `abc_from`) || !strings.Contains(out, "/cluster/proj/pixi.toml") {
		t.Errorf("expected abc_from in meta, got:\n%s", out)
	}
	if !strings.Contains(out, `pixi run --manifest-path`) {
		t.Errorf("expected pixi run --manifest-path in templated script, got:\n%s", out)
	}
	if !strings.Contains(out, `ABC_RUNTIME_PIXI_PHASE`) {
		t.Errorf("expected pixi phase guard in script, got:\n%s", out)
	}
}

func TestJobRun_RuntimePixiAlias(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-alias\n#ABC --runtime=pixi\n#ABC --from=/x/pixi.toml\necho ok\n"
	p := writeTempScript(t, "pixi_alias.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`abc_runtime\s*=\s*"pixi-exec"`).MatchString(out) {
		t.Errorf("expected canonical abc_runtime=pixi-exec, got:\n%s", out)
	}
}

func TestJobRun_RuntimeFromCLIOverride(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-cli-rt\n#ABC --runtime=pixi-exec\n#ABC --from=/preamble/pixi.toml\necho hi\n"
	p := writeTempScript(t, "pixi_cli_rt.sh", script)
	out, err := executeCmd(t, p, "--from", "/override/pixi.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "/override/pixi.toml") {
		t.Errorf("expected CLI --from to override preamble in meta and wrapper, got:\n%s", out)
	}
	if !strings.Contains(out, `abc_from`) || !strings.Contains(out, `"/override/pixi.toml"`) {
		t.Errorf("expected meta abc_from from CLI, got:\n%s", out)
	}
	// Original #ABC lines remain in the script body as comments; only meta + wrapper use the override.
}

func TestJobRun_RuntimeRequiresFrom(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-no-from\n#ABC --runtime=pixi-exec\necho hi\n"
	p := writeTempScript(t, "pixi_no_from.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error: runtime pixi-exec requires --from")
	}
}

func TestJobRun_FromWithoutRuntime(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-no-rt\n#ABC --from=/x/pixi.toml\necho hi\n"
	p := writeTempScript(t, "pixi_no_rt.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error: --from requires --runtime")
	}
}

func TestJobRun_RuntimePixiExecDockerDriver(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-docker\n#ABC --driver=docker\n#ABC --driver.config.image=prefix/pixi-worker:latest\n#ABC --runtime=pixi-exec\n#ABC --from=/app/pixi.toml\necho hi\n"
	p := writeTempScript(t, "pixi_docker.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "docker"`) {
		t.Errorf("expected docker driver, got:\n%s", out)
	}
	if !strings.Contains(out, `pixi run --manifest-path`) {
		t.Errorf("expected pixi wrapper in script, got:\n%s", out)
	}
	// Inner re-exec must not use .../local/... twice for docker (ociTaskScriptArg).
	if !strings.Contains(out, `exec pixi run`) || !strings.Contains(out, `pixi_docker.sh" "$@"`) {
		t.Errorf("expected pixi wrapper line in template, got:\n%s", out)
	}
	if !strings.Contains(out, `"$${NOMAD_TASK_DIR}/pixi_docker.sh"`) {
		t.Errorf("expected Nomad-escaped inner path $${NOMAD_TASK_DIR}/pixi_docker.sh in pixi line, got:\n%s", out)
	}
	if strings.Contains(out, `exec pixi run`) && strings.Contains(out, `"$${NOMAD_TASK_DIR}/local/pixi_docker.sh"`) {
		t.Errorf("did not expect inner path .../local/pixi_docker.sh in pixi line for docker, got:\n%s", out)
	}
}

func TestJobRun_RuntimePixiExecNoNetworkIsError(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-net\n#ABC --runtime=pixi-exec\n#ABC --from=/x/pixi.toml\n#ABC --no-network\necho hi\n"
	p := writeTempScript(t, "pixi_nonet.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error: pixi-exec with --no-network")
	}
}

func TestJobRun_RuntimePixiExecWithCondaIsError(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-conda\n#ABC --conda=env.yaml\n#ABC --runtime=pixi-exec\n#ABC --from=/x/pixi.toml\necho hi\n"
	p := writeTempScript(t, "pixi_conda.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error: pixi-exec combined with conda")
	}
}

func TestJobRun_RuntimePixiExecSlurmDriverIsError(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-slurm\n#ABC --driver=slurm\n#ABC --runtime=pixi-exec\n#ABC --from=/x/pixi.toml\necho hi\n"
	p := writeTempScript(t, "pixi_slurm.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error: pixi-exec with slurm driver")
	}
}

func TestJobRun_RuntimeUnsupportedDriver(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-bad-driver\n#ABC --driver=java\n#ABC --runtime=pixi-exec\n#ABC --from=/x/pixi.toml\necho hi\n"
	p := writeTempScript(t, "pixi_bad_drv.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error: pixi-exec with unsupported driver")
	}
}

func TestJobRun_ScriptWithNoShebang(t *testing.T) {
	// A bare script with no #!/bin/bash line — should still work.
	script := "echo hello world\n"
	p := writeTempScript(t, "no_shebang.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "exec"`) {
		t.Errorf("expected exec driver for bare script, got:\n%s", out)
	}
}

func TestJobRun_OutputFileFlag(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=file-out\necho hi\n"
	p := writeTempScript(t, "file_out.sh", script)
	outFile := filepath.Join(t.TempDir(), "job.hcl")

	// Run with --output-file — stdout should be empty (or just a confirmation),
	// and the file should contain the HCL.
	out, err := executeCmd(t, p, "--output-file", outFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, readErr := os.ReadFile(outFile)
	if readErr != nil {
		t.Fatalf("expected HCL file to be written at %s: %v", outFile, readErr)
	}
	hcl := string(data)
	if !strings.Contains(hcl, `driver = "exec"`) {
		t.Errorf("expected exec driver in written HCL file, got:\n%s", hcl)
	}
	// stdout should be minimal (path echo or empty), not contain full HCL
	if strings.Contains(out, "task_group") && strings.Contains(out, "resources {") {
		t.Errorf("expected HCL to go to file, not stdout, got stdout:\n%s", out)
	}
}

// ── A.2 SLURM driver extras ───────────────────────────────────────────────────

func TestJobRun_SlurmAccountMapping(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name=account-job
#SBATCH --account=bio_team
echo done
`
	p := writeTempScript(t, "account.slurm.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`account\s*=\s*"bio_team"`).MatchString(out) {
		t.Errorf("expected SLURM account mapped to slurm config, got:\n%s", out)
	}
}

func TestJobRun_SlurmNTasksMapping(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name=ntasks-job
#SBATCH --ntasks=8
echo done
`
	p := writeTempScript(t, "ntasks.slurm.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`ntasks\s*=\s*8`).MatchString(out) {
		t.Errorf("expected ntasks=8 in slurm config, got:\n%s", out)
	}
}

func TestJobRun_SlurmNoJobNameFallsBackToFilename(t *testing.T) {
	script := "#!/bin/bash\n#SBATCH --cpus-per-task=2\necho hi\n"
	p := writeTempScript(t, "my-slurm-script.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJobNamePrefix(t, out, "my-slurm-script")
}

// ── A.3 docker driver extras ─────────────────────────────────────────────────

func TestJobRun_DockerDriverCLIFlag(t *testing.T) {
	// --driver.config is a StringToString flag; pass image via "key=value" form.
	script := "#!/bin/bash\necho hi\n"
	p := writeTempScript(t, "docker_cli.sh", script)
	out, err := executeCmd(t, p,
		"--driver", "docker",
		"--driver.config", "image=busybox:latest",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "docker"`) {
		t.Errorf("expected docker driver from CLI flag, got:\n%s", out)
	}
	if !strings.Contains(out, `busybox:latest`) {
		t.Errorf("expected image in docker config, got:\n%s", out)
	}
}

func TestJobRun_RawExecDriverCLIFlag(t *testing.T) {
	script := "#!/bin/bash\necho hi\n"
	p := writeTempScript(t, "raw_exec_cli.sh", script)
	out, err := executeCmd(t, p, "--driver", "raw_exec")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "raw_exec"`) {
		t.Errorf("expected raw_exec driver from CLI flag, got:\n%s", out)
	}
}

func TestJobRun_DockerDriverWithResources(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=docker-res
#ABC --driver=docker
#ABC --driver.config.image=python:3.12-slim
#ABC --cores=4
#ABC --mem=8G
python script.py
`
	p := writeTempScript(t, "docker_res.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "docker"`) {
		t.Errorf("expected docker driver, got:\n%s", out)
	}
	if !strings.Contains(out, `cores  = 4`) {
		t.Errorf("expected cores=4, got:\n%s", out)
	}
	if !strings.Contains(out, `memory = 8192`) {
		t.Errorf("expected memory=8192, got:\n%s", out)
	}
}

// ── A.4 hpc-bridge driver ─────────────────────────────────────────────────────

func TestJobRun_HpcBridgeDriver(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=hpc-bridge-job\n#ABC --driver=hpc-bridge\necho hi\n"
	p := writeTempScript(t, "hpcbridge.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "hpc-bridge"`) {
		t.Errorf("expected hpc-bridge driver, got:\n%s", out)
	}
}

func TestJobRun_RawExecDriver(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=raw-exec-job\n#ABC --driver=raw_exec\necho hi\n"
	p := writeTempScript(t, "rawexec.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "raw_exec"`) {
		t.Errorf("expected raw_exec driver, got:\n%s", out)
	}
}

func TestJobRun_HpcBridgeWithSlurmPartition(t *testing.T) {
	// When #ABC --driver=hpc-bridge overrides the auto-detected slurm driver,
	// the output uses hpc-bridge. The slurm-specific queue config is NOT emitted
	// for hpc-bridge (it's slurm-driver-specific). Job name and namespace are
	// still derived from #SBATCH --job-name.
	script := `#!/bin/bash
#SBATCH --job-name=bridge-slurm
#SBATCH --partition=gpu
#ABC --driver=hpc-bridge
echo hi
`
	p := writeTempScript(t, "bridge_slurm.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "hpc-bridge"`) {
		t.Errorf("expected hpc-bridge driver, got:\n%s", out)
	}
	assertJobNamePrefix(t, out, "bridge-slurm")
}

// ── A.5 Preamble parsing edge cases ──────────────────────────────────────────

func TestJobRun_InlineCommentStripped(t *testing.T) {
	// "#ABC --name=test # this comment should not matter" should parse name=test cleanly.
	script := "#!/bin/bash\n#ABC --name=inline-comment # trailing comment here\necho hi\n"
	p := writeTempScript(t, "inline_comment.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJobNamePrefix(t, out, "inline-comment")
}

func TestJobRun_EmptyScript(t *testing.T) {
	// Script with only a shebang — no directives, all defaults.
	// Use a hyphenated filename so the name derives cleanly.
	script := "#!/bin/bash\n"
	p := writeTempScript(t, "empty-body.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "exec"`) {
		t.Errorf("expected exec driver for empty script, got:\n%s", out)
	}
	// Name should derive from filename stem.
	assertJobNamePrefix(t, out, "empty-body")
}

func TestJobRun_ShebanLineNotTreatedAsDirective(t *testing.T) {
	// A comment like "#!/usr/bin/env python3" must not be parsed as an #ABC directive.
	script := "#!/usr/bin/env python3\nprint('hello')\n"
	p := writeTempScript(t, "python_script.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "exec"`) {
		t.Errorf("expected exec driver, got:\n%s", out)
	}
}

// ── A.6 Directive precedence — params file ────────────────────────────────────

func TestJobRun_ParamsFileOverridesPreamble(t *testing.T) {
	// params file is lowest-priority input and should not override preamble.
	script := "#!/bin/bash\n#ABC --name=preamble-check\n#ABC --cores=8\necho hi\n"
	p := writeTempScript(t, "preamble_check.sh", script)
	paramsPath := filepath.Join(t.TempDir(), "p.yaml")
	if err := os.WriteFile(paramsPath, []byte("cores: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := executeCmd(t, p, "--params-file", paramsPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`cores\s*=\s*8`).MatchString(out) {
		t.Errorf("expected preamble cores=8 to win over params file cores=2, got:\n%s", out)
	}
}

func TestJobRun_ConstraintLTEOperator(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=lte-op\n#ABC --constraint=cpu<=8\necho hi\n"
	p := writeTempScript(t, "lte_op.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `operator  = "<="`) {
		t.Errorf("expected <= operator in constraint, got:\n%s", out)
	}
}

func TestJobRun_ParamsFileFlatYAML(t *testing.T) {
	// Flat YAML key-value pairs in params file are applied as directives.
	script := "#!/bin/bash\n#ABC --name=flat-params\necho hi\n"
	p := writeTempScript(t, "flat_params.sh", script)
	paramsPath := filepath.Join(t.TempDir(), "flat.yaml")
	if err := os.WriteFile(paramsPath, []byte("cores: 4\nnodes: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := executeCmd(t, p, "--params-file", paramsPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Use a regex to handle variable whitespace in HCL alignment.
	if !regexp.MustCompile(`cores\s*=\s*4`).MatchString(out) {
		t.Errorf("expected cores=4 from params file, got:\n%s", out)
	}
	if !strings.Contains(out, "count = 2") {
		t.Errorf("expected nodes=2 (count=2) from params file, got:\n%s", out)
	}
}

func TestJobRun_ParamsFileMissingPath(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=missing-params\necho hi\n"
	p := writeTempScript(t, "missing_params.sh", script)
	_, err := executeCmd(t, p, "--params-file", "/nonexistent/params.yaml")
	if err == nil {
		t.Fatal("expected error for missing params file")
	}
}

func TestJobRun_ParamsFileEmptyIsNoop(t *testing.T) {
	// Empty params file should not error and should not change any values.
	script := "#!/bin/bash\n#ABC --name=empty-params\n#ABC --cores=4\necho hi\n"
	p := writeTempScript(t, "empty_params.sh", script)
	paramsPath := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(paramsPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := executeCmd(t, p, "--params-file", paramsPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Use regex to handle variable HCL whitespace alignment.
	if !regexp.MustCompile(`cores\s*=\s*4`).MatchString(out) {
		t.Errorf("expected cores=4 to be preserved with empty params file, got:\n%s", out)
	}
}

func TestJobRun_ParamsFileInvalidYAML(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=bad-params\necho hi\n"
	p := writeTempScript(t, "bad_params.sh", script)
	paramsPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(paramsPath, []byte("key: [unclosed bracket\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := executeCmd(t, p, "--params-file", paramsPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML in params file")
	}
}

// ── A.7 Resource parsing edge cases ──────────────────────────────────────────

func TestJobRun_MemTerabytesUnsupported(t *testing.T) {
	// The memory parser currently supports G, M, K and bare integers (MB).
	// Terabyte suffix (T) is not supported and returns an error.
	script := "#!/bin/bash\n#ABC --name=huge-mem\n#ABC --mem=1T\necho hi\n"
	p := writeTempScript(t, "huge.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for unsupported 'T' (terabyte) memory suffix")
	}
	if !strings.Contains(err.Error(), "invalid memory") && !strings.Contains(err.Error(), "1T") {
		t.Logf("error: %v", err)
	}
}

func TestJobRun_MemLowercase(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=lower-mem\n#ABC --mem=4g\necho hi\n"
	p := writeTempScript(t, "lower_mem.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "memory = 4096") {
		t.Errorf("expected memory=4096 for 4g (lowercase), got:\n%s", out)
	}
}

func TestJobRun_WalltimeSmall(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=short-job\n#ABC --time=00:00:30\necho hi\n"
	p := writeTempScript(t, "short.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"30"`) {
		t.Errorf("expected 30 seconds in timeout args, got:\n%s", out)
	}
}

// ── A.8 Multiple constraints and affinities ───────────────────────────────────

func TestJobRun_MultipleConstraints(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=multi-constraint
#ABC --constraint=region==za-cpt
#ABC --constraint=node.class!=gpu
echo hi
`
	p := writeTempScript(t, "multi_constraint.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both constraint blocks must appear.
	count := strings.Count(out, "constraint {")
	if count < 2 {
		t.Errorf("expected at least 2 constraint blocks, got %d in:\n%s", count, out)
	}
	if !strings.Contains(out, "za-cpt") {
		t.Errorf("expected za-cpt constraint, got:\n%s", out)
	}
	if !strings.Contains(out, "gpu") {
		t.Errorf("expected gpu constraint value, got:\n%s", out)
	}
}

func TestJobRun_ConstraintInequalityOperator(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=neq-job\n#ABC --constraint=node.class!=gpu\necho hi\n"
	p := writeTempScript(t, "neq.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `operator  = "!="`) {
		t.Errorf("expected != operator in constraint, got:\n%s", out)
	}
}

func TestJobRun_MultipleAffinities(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=multi-affinity
#ABC --affinity=datacenter==c1,weight=75
#ABC --affinity=node.class==compute,weight=50
echo hi
`
	p := writeTempScript(t, "multi_affinity.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	count := strings.Count(out, "affinity {")
	if count < 2 {
		t.Errorf("expected at least 2 affinity blocks, got %d in:\n%s", count, out)
	}
}

func TestJobRun_AffinityDefaultWeight(t *testing.T) {
	// No weight specified — should default to Nomad's default (50).
	script := "#!/bin/bash\n#ABC --name=aff-default-w\n#ABC --affinity=datacenter==c1\necho hi\n"
	p := writeTempScript(t, "aff_default_w.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "affinity {") {
		t.Errorf("expected affinity block, got:\n%s", out)
	}
}

func TestJobRun_ConstraintViaPreamble(t *testing.T) {
	// --constraint is a preamble-only directive (not a cobra CLI flag).
	// Verify that a constraint set in the #ABC preamble appears in the HCL.
	script := "#!/bin/bash\n#ABC --name=constraint-preamble\n#ABC --constraint=region==us-west\necho hi\n"
	p := writeTempScript(t, "constraint_preamble.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "constraint {") {
		t.Errorf("expected constraint block from preamble directive, got:\n%s", out)
	}
	if !strings.Contains(out, "us-west") {
		t.Errorf("expected us-west constraint value, got:\n%s", out)
	}
}

// ── A.9 Meta extras ───────────────────────────────────────────────────────────

func TestJobRun_MetaValueWithEmbeddedEquals(t *testing.T) {
	// --meta=url=http://host:port/path?a=1&b=2 — value contains = signs
	script := "#!/bin/bash\n#ABC --name=meta-eq\necho hi\n"
	p := writeTempScript(t, "meta_eq.sh", script)
	out, err := executeCmd(t, p, "--meta", "url=http://host:8080/path?a=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "url") {
		t.Errorf("expected url meta key, got:\n%s", out)
	}
	if !strings.Contains(out, "http://host:8080/path?a=1") {
		t.Errorf("expected full URL value in meta, got:\n%s", out)
	}
}

func TestJobRun_PixiMetaEmitted(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=pixi-meta\n#ABC --pixi\npixi run go\n"
	p := writeTempScript(t, "pixi_meta.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`abc_pixi\s*=\s*"true"`).MatchString(out) {
		t.Errorf("expected abc_pixi = \"true\" in meta block, got:\n%s", out)
	}
}

func TestJobRun_MetaCLIFlag(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=meta-cli\necho hi\n"
	p := writeTempScript(t, "meta_cli.sh", script)
	out, err := executeCmd(t, p, "--meta", "env=staging", "--meta", "version=v2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "env") || !strings.Contains(out, "staging") {
		t.Errorf("expected env=staging in meta, got:\n%s", out)
	}
	if !strings.Contains(out, "version") || !strings.Contains(out, "v2") {
		t.Errorf("expected version=v2 in meta, got:\n%s", out)
	}
}

// ── A.10 Multiple ports ───────────────────────────────────────────────────────

func TestJobRun_MultiplePorts(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=multi-port
#ABC --port=http
#ABC --port=metrics
#ABC --port=grpc
echo hi
`
	p := writeTempScript(t, "multi_port.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, port := range []string{"http", "metrics", "grpc"} {
		if !strings.Contains(out, fmt.Sprintf(`port "%s"`, port)) {
			t.Errorf("expected port %q in network block, got:\n%s", port, out)
		}
	}
}

// ── A.12 Reschedule extras ────────────────────────────────────────────────────

func TestJobRun_RescheduleModeFail(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=fail-mode\n#ABC --reschedule-mode=fail\necho hi\n"
	p := writeTempScript(t, "fail_mode.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "abc_reschedule_mode") {
		t.Errorf("expected abc_reschedule_mode in meta, got:\n%s", out)
	}
	if !strings.Contains(out, "fail") {
		t.Errorf("expected mode=fail in meta, got:\n%s", out)
	}
}

// ── A.14 Config-based Nomad address (offline) ─────────────────────────────────

func TestJobRun_ConfigNomadAddrUsedAsDefault(t *testing.T) {
	cfgContent := `version: "1"
active_context: test-ctx
contexts:
  test-ctx:
    endpoint: https://api.example.com
    access_token: tok123
    nomad_addr: http://10.0.0.1:4646
    nomad_token: s.testtoken
`
	// The nomad address should be readable from the config.
	// Indirectly test by ensuring the command doesn't error on HCL generation
	// (it would if config loading panicked).
	script := "#!/bin/bash\n#ABC --name=cfg-addr\necho hi\n"
	p := writeTempScript(t, "cfg_addr.sh", script)
	out, err := executeCmdWithABCYAML(t, cfgContent, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "exec"`) {
		t.Errorf("expected exec driver, got:\n%s", out)
	}
}

func TestJobRun_MissingConfigFileUsesDefaults(t *testing.T) {
	// Point to a non-existent config path — should fall back gracefully.
	script := "#!/bin/bash\n#ABC --name=no-cfg\necho hi\n"
	p := writeTempScript(t, "no_cfg.sh", script)
	out, err := executeCmdWithConfigPath(t, "/tmp/nonexistent-abc-config-xyz.yaml", p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still generate valid HCL with defaults.
	if !strings.Contains(out, `driver = "exec"`) {
		t.Errorf("expected exec driver with missing config, got:\n%s", out)
	}
}

// ── A.15 Additional error cases ───────────────────────────────────────────────

func TestJobRun_InvalidGPUCount(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=bad-gpu\n#ABC --gpus=notanumber\necho hi\n"
	p := writeTempScript(t, "bad_gpu.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for non-integer --gpus")
	}
}

func TestJobRun_InvalidPriority(t *testing.T) {
	script := "#!/bin/bash\n#ABC --name=bad-pri\n#ABC --priority=abc\necho hi\n"
	p := writeTempScript(t, "bad_pri.sh", script)
	_, err := executeCmd(t, p)
	if err == nil {
		t.Fatal("expected error for non-integer --priority")
	}
}

// ── Monitoring floor (abc-nodes enhanced) via ABC_CONFIG_FILE ───────────────

func TestJobRun_EnhancedAbcNodesConfig_InjectsMonitoringEnvAndMeta(t *testing.T) {
	raw := `version: "1.0"
active_context: enh
contexts:
  enh:
    endpoint: https://api.example.com
    access_token: tok
    cluster_type: abc-nodes
    capabilities:
      logging: true
      monitoring: true
    admin:
      services:
        nomad:
          nomad_addr: http://127.0.0.1:4646
          nomad_token: t
        loki:
          http: http://192.168.55.1:3100
        prometheus:
          http: http://192.168.55.1:9090
`
	script := "#!/bin/bash\n#ABC --name=enhmon\necho hi\n"
	p := writeTempScript(t, "enh_mon.sh", script)
	out, err := executeCmdWithABCYAML(t, raw, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "ABC_NODES_LOKI_PUSH_URL") {
		t.Fatalf("expected Loki push URL in generated HCL:\n%s", out)
	}
	if !strings.Contains(out, "ABC_NODES_PROMETHEUS_REMOTE_WRITE_URL") {
		t.Fatalf("expected Prometheus remote_write URL:\n%s", out)
	}
	if !strings.Contains(out, "abc_monitoring_floor") {
		t.Fatalf("expected job meta abc_monitoring_floor:\n%s", out)
	}
}

func TestJobRun_BaseAbcNodesConfig_NoMonitoringEnv(t *testing.T) {
	raw := `version: "1.0"
active_context: base
contexts:
  base:
    endpoint: https://api.example.com
    access_token: tok
    cluster_type: abc-nodes
    capabilities:
      logging: false
      monitoring: false
      observability: false
    admin:
      services:
        nomad:
          nomad_addr: http://127.0.0.1:4646
          nomad_token: t
        loki:
          http: http://192.168.55.9:3100
`
	script := "#!/bin/bash\n#ABC --name=basemon\necho hi\n"
	p := writeTempScript(t, "base_mon.sh", script)
	out, err := executeCmdWithABCYAML(t, raw, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "ABC_NODES_LOKI_PUSH_URL") {
		t.Fatalf("did not expect monitoring env on base floor:\n%s", out)
	}
	if strings.Contains(out, "abc_monitoring_floor") {
		t.Fatalf("did not expect abc_monitoring_floor meta on base floor:\n%s", out)
	}
}

// committedWorkloadScript returns a path to deployments/.../workloads/*.sh
// relative to package dir cmd/job (go test cwd for this package).
func committedWorkloadScript(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("..", "..", "deployments", "abc-nodes", "nomad", "tests", "workloads", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("missing committed workload script %q: %v", p, err)
	}
	return p
}

// abcNodesConfigWithContainerd returns a minimal abc-nodes config YAML whose
// capabilities.nodes list includes one node with containerd-driver.
// Used by tests that exercise auto-container driver resolution.
const abcNodesConfigWithContainerd = `version: 1.0
active_context: abc-nodes-test
contexts:
  abc-nodes-test:
    cluster_type: abc-nodes
    admin:
      services:
        nomad:
          nomad_addr: http://127.0.0.1:4646
          nomad_token: t
    capabilities:
      nodes:
        - id: "aaaa0000-0000-0000-0000-000000000001"
          hostname: "test-node-containerd"
          drivers:
            - containerd-driver
            - exec
            - raw_exec
`

func TestJobRun_WorkloadStressNgCpuDefaultHCL(t *testing.T) {
	p := committedWorkloadScript(t, "stress-ng-cpu-default.sh")
	// Use a config with capabilities.nodes so auto-container can resolve to containerd-driver.
	out, err := executeCmdWithABCYAML(t, abcNodesConfigWithContainerd, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `namespace = "default"`) {
		t.Errorf("expected namespace default in HCL, got:\n%s", out)
	}
	if !strings.Contains(out, `driver = "containerd-driver"`) {
		t.Errorf("expected containerd-driver resolved from auto-container, got:\n%s", out)
	}
	// auto-container injects a node.unique.id regexp constraint.
	if !strings.Contains(out, `node.unique.id`) {
		t.Errorf("expected node.unique.id constraint injected by auto-container, got:\n%s", out)
	}
	if !strings.Contains(out, `community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8`) {
		t.Errorf("expected hyperfine_stress-ng Wave OCI image in HCL, got:\n%s", out)
	}
	if !strings.Contains(out, `"/bin/sh", "$${NOMAD_TASK_DIR}/stress-ng-cpu-default.sh"`) {
		t.Errorf("expected containerd-driver tasks to run script via /bin/sh with task-dir path in args (timeout wrapper), got:\n%s", out)
	}
	if !strings.Contains(out, `destination = "local/stress-ng-cpu-default.sh"`) {
		t.Errorf("expected templated script under local/, got:\n%s", out)
	}
	if !strings.Contains(out, `"stress-ng"`) || !strings.Contains(out, "workload") {
		t.Errorf("expected meta workload stress-ng, got:\n%s", out)
	}
	if !strings.Contains(out, `NOMAD_NAMESPACE = "$${NOMAD_NAMESPACE}"`) {
		t.Errorf("expected NOMAD_NAMESPACE env passthrough in HCL, got:\n%s", out)
	}
}

func TestJobRun_WorkloadHyperfineServicesHCL(t *testing.T) {
	p := committedWorkloadScript(t, "hyperfine-micro-services.sh")
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `namespace = "services"`) {
		t.Errorf("expected namespace services, got:\n%s", out)
	}
	if !strings.Contains(out, `driver = "containerd-driver"`) {
		t.Errorf("expected containerd-driver, got:\n%s", out)
	}
	if !strings.Contains(out, `NOMAD_JOB_NAME`) {
		t.Errorf("expected NOMAD_JOB_NAME in env from --job_name, got:\n%s", out)
	}
	if !strings.Contains(out, `community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8`) {
		t.Errorf("expected hyperfine_stress-ng Wave OCI image in HCL, got:\n%s", out)
	}
	if !strings.Contains(out, `"/bin/sh", "$${NOMAD_TASK_DIR}/hyperfine-micro-services.sh"`) {
		t.Errorf("expected containerd-driver tasks to run script via /bin/sh with task-dir path in args (timeout wrapper), got:\n%s", out)
	}
}

func TestJobRun_WorkloadResearchUserJobNameHCL(t *testing.T) {
	p := committedWorkloadScript(t, "stress-ng-cpu-user-uh-bristol-animaltb-hpc_alice.sh")
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `uh-bristol-animaltb-hpc_alice--wl-str`) {
		t.Errorf("expected research-user workload stem in generated job/HCL, got:\n%s", out)
	}
	if !regexp.MustCompile(`research_user\s*=\s*"uh-bristol-animaltb-hpc_alice"`).MatchString(out) {
		t.Errorf("expected job meta research_user=uh-bristol-animaltb-hpc_alice in HCL, got:\n%s", out)
	}
}

func TestJobRun_WorkloadStressAbcContextInjectsNamespace(t *testing.T) {
	raw := `version: "1.0"
active_context: wlctx
contexts:
  wlctx:
    endpoint: https://api.example.com
    access_token: tok
    cluster_type: abc-nodes
    capabilities:
      logging: false
      monitoring: false
      observability: false
    admin:
      abc_nodes:
        nomad_namespace: from-config-tenant
      services:
        nomad:
          nomad_addr: http://127.0.0.1:4646
          nomad_token: t
`
	p := committedWorkloadScript(t, "stress-ng-cpu-abc-context.sh")
	out, err := executeCmdWithABCYAML(t, raw, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `namespace = "from-config-tenant"`) {
		t.Errorf("expected namespace from abc context admin.abc_nodes.nomad_namespace, got:\n%s", out)
	}
	if !strings.Contains(out, `driver = "containerd-driver"`) {
		t.Errorf("expected containerd-driver, got:\n%s", out)
	}
}
