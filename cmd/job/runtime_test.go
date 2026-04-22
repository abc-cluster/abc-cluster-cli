package job

import (
	"strings"
	"testing"
)

func TestShellSingleQuote(t *testing.T) {
	if got := shellSingleQuote(`a'b`); got != `'a'"'"'b'` {
		t.Fatalf("shellSingleQuote: got %q", got)
	}
}

func TestPrependPixiWorkspaceWrapper(t *testing.T) {
	script := `#!/bin/bash
#ABC --name=x
echo hi
`
	out, err := prependPixiWorkspaceWrapper(script, "job.sh", "/work/proj/pixi.toml", "exec")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `exec pixi run --manifest-path '/work/proj/pixi.toml'`) {
		t.Fatalf("expected pixi run manifest line, got:\n%s", out)
	}
	if !strings.Contains(out, `"local/job.sh"`) {
		t.Fatalf("expected quoted local/job.sh for exec driver, got:\n%s", out)
	}
	if !strings.Contains(out, `ABC_RUNTIME_PIXI_PHASE`) {
		t.Fatalf("expected phase guard, got:\n%s", out)
	}
	if !strings.Contains(out, "echo hi") {
		t.Fatalf("expected body preserved, got:\n%s", out)
	}
}

func TestNormalizeRuntimeID(t *testing.T) {
	if got := NormalizeRuntimeID("  PiXi  "); got != runtimePixiExec {
		t.Fatalf("pixi alias: got %q", got)
	}
	if got := NormalizeRuntimeID("pixi-exec"); got != runtimePixiExec {
		t.Fatalf("canonical: got %q", got)
	}
}

func TestValidateRuntimeDriver(t *testing.T) {
	sp := func(driver, runtime, from string) *jobSpec {
		return &jobSpec{Driver: driver, Runtime: runtime, From: from}
	}
	if err := ValidateRuntimeDriver(sp("exec", "pixi-exec", "/x/pixi.toml")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeDriver(sp("", "", "")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeDriver(sp("exec", "", "/x/pixi.toml")); err == nil {
		t.Fatal("expected error: from without runtime")
	}
	if err := ValidateRuntimeDriver(sp("exec", "pixi-exec", "")); err == nil {
		t.Fatal("expected error: runtime without from")
	}
	if err := ValidateRuntimeDriver(sp("exec", "pixi-exec", "/bad")); err == nil {
		t.Fatal("expected error: from not .toml")
	}
	if err := ValidateRuntimeDriver(sp("qemu", "pixi-exec", "/x/pixi.toml")); err == nil {
		t.Fatal("expected error: unsupported driver")
	}
	if err := ValidateRuntimeDriver(sp("exec", "wave", "x")); err == nil {
		t.Fatal("expected error: unknown runtime")
	}
	if err := ValidateRuntimeDriver(sp("slurm", "pixi-exec", "/x/pixi.toml")); err == nil {
		t.Fatal("expected error: slurm + pixi-exec")
	}
	spNoNet := &jobSpec{Driver: "exec", Runtime: "pixi-exec", From: "/x/pixi.toml", NoNetwork: true}
	if err := ValidateRuntimeDriver(spNoNet); err == nil {
		t.Fatal("expected error: no-network + pixi-exec")
	}
	spConda := &jobSpec{Driver: "exec", Runtime: "pixi-exec", From: "/x/pixi.toml", Conda: "env.yaml"}
	if err := ValidateRuntimeDriver(spConda); err == nil {
		t.Fatal("expected error: conda + pixi-exec")
	}
	spBadFrom := &jobSpec{Driver: "exec", Runtime: "pixi-exec", From: "/x/pixi.toml\nevil"}
	if err := ValidateRuntimeDriver(spBadFrom); err == nil {
		t.Fatal("expected error: newline in from")
	}
}

func TestPrependPixiWorkspaceWrapperDockerInnerPath(t *testing.T) {
	script := "#!/bin/bash\necho hi\n"
	out, err := prependPixiWorkspaceWrapper(script, "app.sh", "/app/pixi.toml", "docker")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"${NOMAD_TASK_DIR}/app.sh"`) {
		t.Fatalf("expected docker-style inner path without extra local/, got:\n%s", out)
	}
	if strings.Contains(out, `exec pixi run`) && strings.Contains(out, `"${NOMAD_TASK_DIR}/local/app.sh"`) {
		t.Fatalf("did not expect inner re-exec to use .../local/app.sh for docker, got:\n%s", out)
	}
}
