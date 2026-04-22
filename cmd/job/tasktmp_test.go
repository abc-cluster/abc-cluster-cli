package job

import (
	"strings"
	"testing"
)

func TestPrependTaskTmpIfNeeded(t *testing.T) {
	sp := &jobSpec{TaskTmp: true}
	script := "#!/bin/bash\necho ok\n"
	out := prependTaskTmpIfNeeded(script, sp)
	if !strings.Contains(out, `mkdir -p "${NOMAD_TASK_DIR}/tmp"`) {
		t.Fatalf("expected mkdir tmp, got:\n%s", out)
	}
	if !strings.Contains(out, `export TMPDIR="${NOMAD_TASK_DIR}/tmp"`) {
		t.Fatalf("expected TMPDIR export, got:\n%s", out)
	}
	if !strings.Contains(out, "echo ok") {
		t.Fatalf("expected body preserved, got:\n%s", out)
	}
}

func TestPrependTaskTmpDisabled(t *testing.T) {
	sp := &jobSpec{TaskTmp: false}
	script := "#!/bin/bash\necho ok\n"
	if got := prependTaskTmpIfNeeded(script, sp); got != script {
		t.Fatalf("expected unchanged script")
	}
}
