package job

import (
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

func mkTaskWithScript(args []interface{}, templates []utils.NomadTemplate) *NomadTask {
	return &NomadTask{
		Name:      "main",
		Driver:    "docker",
		Config:    map[string]interface{}{"command": "timeout", "args": args},
		Templates: templates,
	}
}

func TestExtractEntrypointScript(t *testing.T) {
	t.Run("matches template by basename from config.args", func(t *testing.T) {
		task := mkTaskWithScript(
			[]interface{}{"300", "/bin/sh", "${NOMAD_TASK_DIR}/hello.sh"},
			[]utils.NomadTemplate{
				{DestPath: "local/hello.sh", EmbeddedTmpl: "#!/bin/sh\necho hi\n"},
				{DestPath: "secrets/aws.env", EmbeddedTmpl: "AWS_X=1"},
			},
		)
		body, dest, ok := extractEntrypointScript(task)
		if !ok {
			t.Fatal("expected to find script")
		}
		if dest != "local/hello.sh" {
			t.Errorf("dest = %q", dest)
		}
		if body != "#!/bin/sh\necho hi\n" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("falls back to the single .sh template when args don't match", func(t *testing.T) {
		task := mkTaskWithScript(
			[]interface{}{"echo", "no-script-arg-here"},
			[]utils.NomadTemplate{
				{DestPath: "local/run.sh", EmbeddedTmpl: "echo run\n"},
				{DestPath: "local/nextflow.config", EmbeddedTmpl: "process {}"},
			},
		)
		body, dest, ok := extractEntrypointScript(task)
		if !ok || dest != "local/run.sh" || body != "echo run\n" {
			t.Errorf("fallback failed: ok=%v dest=%q body=%q", ok, dest, body)
		}
	})

	t.Run("ambiguous: two .sh templates, no args hint → not found", func(t *testing.T) {
		task := mkTaskWithScript(
			nil,
			[]utils.NomadTemplate{
				{DestPath: "local/a.sh", EmbeddedTmpl: "a"},
				{DestPath: "local/b.sh", EmbeddedTmpl: "b"},
			},
		)
		if _, _, ok := extractEntrypointScript(task); ok {
			t.Error("expected ambiguous case to return not-found")
		}
	})

	t.Run("no templates → not found", func(t *testing.T) {
		task := &NomadTask{Name: "main", Driver: "docker"}
		if _, _, ok := extractEntrypointScript(task); ok {
			t.Error("expected not-found for task with no templates")
		}
	})

	t.Run("picks last .sh arg when args list has several", func(t *testing.T) {
		task := mkTaskWithScript(
			[]interface{}{"/bin/bash", "wrapper.sh", "--", "/work/real-entry.sh"},
			[]utils.NomadTemplate{
				{DestPath: "local/wrapper.sh", EmbeddedTmpl: "wrapper"},
				{DestPath: "local/real-entry.sh", EmbeddedTmpl: "real"},
			},
		)
		body, dest, ok := extractEntrypointScript(task)
		if !ok || dest != "local/real-entry.sh" || body != "real" {
			t.Errorf("expected last .sh arg to win: ok=%v dest=%q body=%q", ok, dest, body)
		}
	})
}

func TestSelectTask(t *testing.T) {
	job := &NomadJob{
		ID: "j",
		TaskGroups: []NomadTaskGroup{
			{Name: "g1", Tasks: []NomadTask{{Name: "alpha"}, {Name: "beta"}}},
		},
	}
	t.Run("default → first task", func(t *testing.T) {
		task, err := selectTask(job, "")
		if err != nil || task.Name != "alpha" {
			t.Errorf("got %v, %v", task, err)
		}
	})
	t.Run("by name", func(t *testing.T) {
		task, err := selectTask(job, "beta")
		if err != nil || task.Name != "beta" {
			t.Errorf("got %v, %v", task, err)
		}
	})
	t.Run("missing name → error", func(t *testing.T) {
		if _, err := selectTask(job, "gamma"); err == nil {
			t.Error("expected error for missing task")
		}
	})
	t.Run("no groups → error", func(t *testing.T) {
		if _, err := selectTask(&NomadJob{ID: "empty"}, ""); err == nil {
			t.Error("expected error for job with no groups")
		}
	})
}

func TestBaseName(t *testing.T) {
	cases := map[string]string{
		"${NOMAD_TASK_DIR}/hello.sh": "hello.sh",
		"local/run.sh":              "run.sh",
		"plain.sh":                  "plain.sh",
		"":                          "",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q; want %q", in, got, want)
		}
	}
}
