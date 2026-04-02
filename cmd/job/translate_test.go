package job

import (
	"os"
	"strings"
	"testing"
)

func TestTranslateJobScript_SlurmBasic(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name=test-job
#SBATCH --cpus-per-task=4
#SBATCH --mem=8G
#SBATCH --time=01:00:00
#SBATCH --partition=batch

echo hi
`

	out, unmapped, err := translateScript(script, "job.slurm.sh", "")
	if err != nil {
		t.Fatalf("translateScript returned error: %v", err)
	}
	if len(unmapped) != 0 {
		t.Fatalf("expected no unmapped on core directives, got %d", len(unmapped))
	}
	if !strings.Contains(out, "#ABC --name=test-job") {
		t.Fatalf("expected name directive mapped; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --cores=4") {
		t.Fatalf("expected cores mapped; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --mem=8G") {
		t.Fatalf("expected mem mapped; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --time=01:00:00") {
		t.Fatalf("expected time mapped; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --dc=batch") {
		t.Fatalf("expected partition->dc mapped; got: %s", out)
	}
}

func TestTranslateJobScript_PBSBasic(t *testing.T) {
	script := `#!/bin/bash
#PBS -N pbs-job
#PBS -l nodes=2:ppn=4,mem=16gb
#PBS -l walltime=02:00:00
#PBS -q workq

echo hello
`

	out, unmapped, err := translateScript(script, "job.pbs.sh", "")
	if err != nil {
		t.Fatalf("translateScript returned error: %v", err)
	}
	if len(unmapped) != 0 {
		t.Fatalf("expected no unmapped on core directives, got %d", len(unmapped))
	}
	if !strings.Contains(out, "#ABC --name=pbs-job") {
		t.Fatalf("expected name mapped; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --nodes=2") {
		t.Fatalf("expected nodes mapped; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --cores=4") {
		t.Fatalf("expected cores mapped; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --mem=16gb") {
		t.Fatalf("expected mem mapped; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --time=02:00:00") {
		t.Fatalf("expected time mapped; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --dc=workq") {
		t.Fatalf("expected queue->dc mapped; got: %s", out)
	}
}

func TestTranslateJobScript_UnmappedPreserved(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --gres=gpu:2

echo bye
`

	out, unmapped, err := translateScript(script, "job.slurm.sh", "")
	if err != nil {
		t.Fatalf("translateScript returned error: %v", err)
	}
	if len(unmapped) != 1 {
		t.Fatalf("expected unmapped count 1, got %d", len(unmapped))
	}
	if !strings.Contains(out, "# NOTE: unmapped directive") {
		t.Fatalf("expected note about unmapped directive; got: %s", out)
	}
	if !strings.Contains(out, "#SBATCH --gres=gpu:2") {
		t.Fatalf("expected original directive preserved; got: %s", out)
	}
}

func TestTranslateJobScript_QuotedValues(t *testing.T) {
	script := `#!/bin/bash
#SBATCH --job-name="my job"
#SBATCH --partition='long queue'

echo quoted
`

	out, unmapped, err := translateScript(script, "job.slurm.sh", "")
	if err != nil {
		t.Fatalf("translateScript returned error: %v", err)
	}
	if len(unmapped) != 0 {
		t.Fatalf("expected no unmapped directives but got %d", len(unmapped))
	}
	if !strings.Contains(out, "#ABC --name='my job'") {
		t.Fatalf("expected quoted name; got: %s", out)
	}
	if !strings.Contains(out, "#ABC --dc='long queue'") {
		t.Fatalf("expected quoted queue; got: %s", out)
	}
}

func TestTranslateJobScript_ExecutorOverride(t *testing.T) {
	script := `#!/bin/bash
#PBS -N pbs-job
#PBS -l nodes=1

echo override
`
	
	cmd := newTranslateCmd()
	bufOut := &strings.Builder{}
	bufErr := &strings.Builder{}
	cmd.SetOut(bufOut)
	cmd.SetErr(bufErr)
	cmd.SetArgs([]string{"--executor", "pbs", "testfile.sh"})

	// stub file content via os.Create temp file because translate cmd reads file path
	tmp, err := os.CreateTemp("", "pbs-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(script)
	tmp.Close()

	cmd.SetArgs([]string{"--executor", "pbs", tmp.Name()})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cmd.Execute failed: %v", err)
	}
	out := bufOut.String()
	if !strings.Contains(out, "#ABC --name=pbs-job") {
		t.Fatalf("expected PBS override translation; got: %s", out)
	}
}

