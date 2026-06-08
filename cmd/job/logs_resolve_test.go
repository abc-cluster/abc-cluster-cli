package job

import (
	"testing"

	"github.com/spf13/cobra"
)

func taskCmd(explicit string) *cobra.Command {
	c := &cobra.Command{}
	c.Flags().String("task", "main", "")
	if explicit != "" {
		_ = c.Flags().Set("task", explicit) // marks the flag Changed
	}
	return c
}

func stub(id string, tasks ...string) *NomadAllocStub {
	ts := map[string]NomadTaskState{}
	for _, t := range tasks {
		ts[t] = NomadTaskState{}
	}
	return &NomadAllocStub{ID: id, TaskStates: ts}
}

func TestResolveAllocTask(t *testing.T) {
	const id = "abcdef1234567890"

	// Default "main" not present, single task → use the single task (the
	// Nextflow head-job case: task is "nextflow", not "main").
	got, err := resolveAllocTask(taskCmd(""), stub(id, "nextflow"), "main")
	if err != nil || got != "nextflow" {
		t.Fatalf("single-task auto-resolve: got %q err %v", got, err)
	}

	// Requested task exists → returned as-is.
	got, err = resolveAllocTask(taskCmd("nextflow"), stub(id, "nextflow"), "nextflow")
	if err != nil || got != "nextflow" {
		t.Fatalf("exact match: got %q err %v", got, err)
	}

	// Multiple tasks, default "main" absent → prefer the known head names.
	got, err = resolveAllocTask(taskCmd(""), stub(id, "nf-task", "logshipper"), "main")
	if err != nil || got != "nf-task" {
		t.Fatalf("multi-task preference: got %q err %v", got, err)
	}

	// Explicit --task that doesn't exist → error listing available tasks.
	if _, err := resolveAllocTask(taskCmd("bogus"), stub(id, "nextflow"), "bogus"); err == nil {
		t.Fatal("explicit missing task should error")
	}

	// Multiple tasks, no preference match, default "main" → ambiguity error.
	if _, err := resolveAllocTask(taskCmd(""), stub(id, "alpha", "beta"), "main"); err == nil {
		t.Fatal("ambiguous multi-task should error")
	}

	// No task states (e.g. list endpoint gave none) → pass through unchanged.
	got, err = resolveAllocTask(taskCmd(""), &NomadAllocStub{ID: id}, "main")
	if err != nil || got != "main" {
		t.Fatalf("no taskstates passthrough: got %q err %v", got, err)
	}
}
