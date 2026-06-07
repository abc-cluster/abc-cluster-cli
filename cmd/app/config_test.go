package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseEnvAssignments_Valid(t *testing.T) {
	got, err := parseEnvAssignments([]string{"LOG_LEVEL=debug", "FOO=bar=baz"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["LOG_LEVEL"] != "debug" {
		t.Errorf("LOG_LEVEL: got %q", got["LOG_LEVEL"])
	}
	// Only the first '=' splits; the rest is the value verbatim.
	if got["FOO"] != "bar=baz" {
		t.Errorf("FOO: got %q want %q", got["FOO"], "bar=baz")
	}
}

func TestParseEnvAssignments_Malformed(t *testing.T) {
	for _, tok := range []string{"NOEQUALS", "=noval"} {
		if _, err := parseEnvAssignments([]string{tok}); err == nil {
			t.Errorf("expected error for %q", tok)
		}
	}
}

func TestParseEnvAssignments_RejectsProtected(t *testing.T) {
	for _, tok := range []string{
		"ABC_PROJECT=x", "AWS_ACCESS_KEY_ID=x", "ABC_MINIO_ENDPOINT=x", "aws_secret_access_key=x",
	} {
		_, err := parseEnvAssignments([]string{tok})
		if err == nil || !strings.Contains(err.Error(), "platform-injected") {
			t.Errorf("expected protected-var rejection for %q, got: %v", tok, err)
		}
	}
}

func TestMergeUserEnv(t *testing.T) {
	current := map[string]string{"A": "1", "B": "2", "AWS_ACCESS_KEY_ID": "secret"}
	updates := map[string]string{"B": "20", "C": "3"}
	merged := mergeUserEnv(current, updates)
	if merged["A"] != "1" || merged["B"] != "20" || merged["C"] != "3" {
		t.Errorf("merge wrong: %v", merged)
	}
	// Protected vars already on the task survive a merge (re-register re-injects).
	if merged["AWS_ACCESS_KEY_ID"] != "secret" {
		t.Errorf("protected var should survive merge: %v", merged)
	}
}

func TestSetAppTaskEnv(t *testing.T) {
	jobJSON := `{
	  "ID": "app-p-a",
	  "TaskGroups": [
	    {"Name": "app", "Tasks": [
	      {"Name": "app", "Env": {"OLD": "x"}}
	    ]}
	  ]
	}`
	var job map[string]interface{}
	if err := json.Unmarshal([]byte(jobJSON), &job); err != nil {
		t.Fatal(err)
	}
	if err := setAppTaskEnv(job, map[string]string{"NEW": "y", "LOG": "info"}); err != nil {
		t.Fatalf("setAppTaskEnv: %v", err)
	}
	task := job["TaskGroups"].([]interface{})[0].(map[string]interface{})["Tasks"].([]interface{})[0].(map[string]interface{})
	env := task["Env"].(map[string]interface{})
	if env["NEW"] != "y" || env["LOG"] != "info" {
		t.Errorf("env not replaced: %v", env)
	}
	if _, ok := env["OLD"]; ok {
		t.Errorf("old env should be replaced wholesale: %v", env)
	}
}

func TestSetAppTaskEnv_NoTasks(t *testing.T) {
	var job map[string]interface{}
	if err := json.Unmarshal([]byte(`{"ID":"x","TaskGroups":[]}`), &job); err != nil {
		t.Fatal(err)
	}
	if err := setAppTaskEnv(job, map[string]string{"A": "1"}); err == nil {
		t.Fatal("expected error for job with no task groups")
	}
}
