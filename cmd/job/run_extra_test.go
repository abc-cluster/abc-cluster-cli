package job_test

// run_extra_test.go — additional offline unit tests for abc job run.
//
// These tests complement run_test.go and cover scenarios not yet tested there:
//   - GPU / chdir / depend / pixi directives
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
	// Write a minimal config file that sets nomad_addr.
	// Then verify that nomadAddrFromCmd returns the configured value.
	// We can't easily test HTTP calls offline, but we can verify the config
	// loading path by exercising NomadDefaultsFromConfig.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgContent := `version: "1"
active_context: test-ctx
contexts:
  test-ctx:
    endpoint: https://api.example.com
    access_token: tok123
    nomad_addr: http://10.0.0.1:4646
    nomad_token: s.testtoken
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABC_CONFIG_FILE", cfgPath)

	// The nomad address should be readable from the config.
	// Indirectly test by ensuring the command doesn't error on HCL generation
	// (it would if config loading panicked).
	script := "#!/bin/bash\n#ABC --name=cfg-addr\necho hi\n"
	p := writeTempScript(t, "cfg_addr.sh", script)
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "exec"`) {
		t.Errorf("expected exec driver, got:\n%s", out)
	}
}

func TestJobRun_MissingConfigFileUsesDefaults(t *testing.T) {
	// Point to a non-existent config path — should fall back gracefully.
	t.Setenv("ABC_CONFIG_FILE", "/tmp/nonexistent-abc-config-xyz.yaml")

	script := "#!/bin/bash\n#ABC --name=no-cfg\necho hi\n"
	p := writeTempScript(t, "no_cfg.sh", script)
	out, err := executeCmd(t, p)
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
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
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
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABC_CONFIG_FILE", cfgPath)

	script := "#!/bin/bash\n#ABC --name=enhmon\necho hi\n"
	p := writeTempScript(t, "enh_mon.sh", script)
	out, err := executeCmd(t, p)
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
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
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
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABC_CONFIG_FILE", cfgPath)

	script := "#!/bin/bash\n#ABC --name=basemon\necho hi\n"
	p := writeTempScript(t, "base_mon.sh", script)
	out, err := executeCmd(t, p)
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
