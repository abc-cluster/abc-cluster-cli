package pipeline_test

import (
"bytes"
"fmt"
"os"
"path/filepath"
"strings"
"testing"

"github.com/abc-cluster/abc-cluster-cli/api"
"github.com/abc-cluster/abc-cluster-cli/cmd/pipeline"
"github.com/spf13/cobra"
)

// mockRunner is a test double for PipelineRunner.
type mockRunner struct {
capturedReq *api.PipelineRunRequest
response    *api.PipelineRunResponse
err         error
}

func (m *mockRunner) SubmitPipelineRun(req *api.PipelineRunRequest) (*api.PipelineRunResponse, error) {
m.capturedReq = req
return m.response, m.err
}

// testFactory returns a ClientFactory that always uses the given mock.
func testFactory(mock *mockRunner) pipeline.ClientFactory {
return func(_, _, _ string) pipeline.PipelineRunner {
return mock
}
}

// executeCmd runs a cobra command with the given args and returns stdout output.
func executeCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
t.Helper()
buf := &bytes.Buffer{}
cmd.SetOut(buf)
cmd.SetErr(buf)
cmd.SetArgs(args)
_, err := cmd.ExecuteC()
return buf.String(), err
}

// buildCmd creates a fresh pipeline command with a mock factory for each test.
func buildCmd(serverURL, accessToken, workspace *string, mock *mockRunner) *cobra.Command {
return pipeline.NewCmd(serverURL, accessToken, workspace, testFactory(mock))
}

func TestPipelineRun_BasicSubmission(t *testing.T) {
mock := &mockRunner{
response: &api.PipelineRunResponse{RunID: "run-abc", RunName: "my-run"},
}
serverURL := "https://api.example.com"
accessToken := "token"
workspace := ""

cmd := buildCmd(&serverURL, &accessToken, &workspace, mock)
out, err := executeCmd(t, cmd, "run", "--pipeline", "https://github.com/org/pipe")
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if mock.capturedReq == nil {
t.Fatal("expected a request to be submitted")
}
if mock.capturedReq.Pipeline != "https://github.com/org/pipe" {
t.Errorf("expected pipeline URL, got %q", mock.capturedReq.Pipeline)
}
if !strings.Contains(out, "run-abc") {
t.Errorf("expected run ID in output, got: %s", out)
}
if !strings.Contains(out, "Pipeline run submitted successfully") {
t.Errorf("expected success message, got: %s", out)
}
}

func TestPipelineRun_WithAllFlags(t *testing.T) {
mock := &mockRunner{
response: &api.PipelineRunResponse{RunID: "run-xyz", RunName: "custom-name", WorkflowID: "wf-1"},
}
serverURL := "https://api.example.com"
accessToken := "token"
workspace := "ws-1"

cmd := buildCmd(&serverURL, &accessToken, &workspace, mock)
out, err := executeCmd(t, cmd, "run",
"--pipeline", "my-pipeline",
"--name", "custom-name",
"--revision", "v1.2",
"--profile", "test,ci",
"--work-dir", "s3://my-bucket/work",
)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}

req := mock.capturedReq
if req.Pipeline != "my-pipeline" {
t.Errorf("Pipeline: got %q, want %q", req.Pipeline, "my-pipeline")
}
if req.RunName != "custom-name" {
t.Errorf("RunName: got %q, want %q", req.RunName, "custom-name")
}
if req.Revision != "v1.2" {
t.Errorf("Revision: got %q, want %q", req.Revision, "v1.2")
}
if req.Profile != "test,ci" {
t.Errorf("Profile: got %q, want %q", req.Profile, "test,ci")
}
if req.WorkDir != "s3://my-bucket/work" {
t.Errorf("WorkDir: got %q, want %q", req.WorkDir, "s3://my-bucket/work")
}
if !strings.Contains(out, "wf-1") {
t.Errorf("expected workflow ID in output, got: %s", out)
}
}

func TestPipelineRun_ShortFlagPipeline(t *testing.T) {
mock := &mockRunner{
response: &api.PipelineRunResponse{RunID: "run-short", RunName: "short"},
}
serverURL := "https://api.example.com"
accessToken := "token"
workspace := ""

cmd := buildCmd(&serverURL, &accessToken, &workspace, mock)
_, err := executeCmd(t, cmd, "run", "-p", "my-pipeline")
if err != nil {
t.Fatalf("unexpected error with short flag: %v", err)
}
if mock.capturedReq.Pipeline != "my-pipeline" {
t.Errorf("expected pipeline 'my-pipeline', got %q", mock.capturedReq.Pipeline)
}
}

func TestPipelineRun_WithParamsYAML(t *testing.T) {
paramsFile := filepath.Join(t.TempDir(), "params.yaml")
if err := os.WriteFile(paramsFile, []byte("input: data.csv\nthreads: 4\n"), 0600); err != nil {
t.Fatal(err)
}

mock := &mockRunner{
response: &api.PipelineRunResponse{RunID: "run-1", RunName: "test"},
}
serverURL := "https://api.example.com"
accessToken := "token"
workspace := ""

cmd := buildCmd(&serverURL, &accessToken, &workspace, mock)
_, err := executeCmd(t, cmd, "run", "--pipeline", "my-pipe", "--params-file", paramsFile)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if mock.capturedReq.Params == nil {
t.Fatal("expected params to be set")
}
if mock.capturedReq.Params["input"] != "data.csv" {
t.Errorf("expected input=data.csv, got %v", mock.capturedReq.Params["input"])
}
}

func TestPipelineRun_WithParamsJSON(t *testing.T) {
paramsFile := filepath.Join(t.TempDir(), "params.json")
if err := os.WriteFile(paramsFile, []byte(`{"genome":"hg38","depth":30}`), 0600); err != nil {
t.Fatal(err)
}

mock := &mockRunner{
response: &api.PipelineRunResponse{RunID: "run-2", RunName: "test"},
}
serverURL := "https://api.example.com"
accessToken := "token"
workspace := ""

cmd := buildCmd(&serverURL, &accessToken, &workspace, mock)
_, err := executeCmd(t, cmd, "run", "--pipeline", "my-pipe", "--params-file", paramsFile)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if mock.capturedReq.Params["genome"] != "hg38" {
t.Errorf("expected genome=hg38, got %v", mock.capturedReq.Params["genome"])
}
}

func TestPipelineRun_MissingPipeline(t *testing.T) {
mock := &mockRunner{
response: &api.PipelineRunResponse{RunID: "run-1", RunName: "test"},
}
serverURL := "https://api.example.com"
accessToken := "token"
workspace := ""

cmd := buildCmd(&serverURL, &accessToken, &workspace, mock)
_, err := executeCmd(t, cmd, "run")
if err == nil {
t.Fatal("expected error for missing --pipeline flag")
}
}

func TestPipelineRun_APIError(t *testing.T) {
mock := &mockRunner{
err: fmt.Errorf("API error (HTTP 400): invalid pipeline"),
}
serverURL := "https://api.example.com"
accessToken := "token"
workspace := ""

cmd := buildCmd(&serverURL, &accessToken, &workspace, mock)
_, err := executeCmd(t, cmd, "run", "--pipeline", "bad-pipeline")
if err == nil {
t.Fatal("expected error from API")
}
if !strings.Contains(err.Error(), "pipeline run submission failed") {
t.Errorf("unexpected error message: %v", err)
}
}

func TestPipelineRun_WithConfigFile(t *testing.T) {
configFile := filepath.Join(t.TempDir(), "nextflow.config")
configContent := "process.cpus = 4\nprocess.memory = '8 GB'\n"
if err := os.WriteFile(configFile, []byte(configContent), 0600); err != nil {
t.Fatal(err)
}

mock := &mockRunner{
response: &api.PipelineRunResponse{RunID: "run-cfg", RunName: "cfg-run"},
}
serverURL := "https://api.example.com"
accessToken := "token"
workspace := ""

cmd := buildCmd(&serverURL, &accessToken, &workspace, mock)
_, err := executeCmd(t, cmd, "run", "--pipeline", "my-pipe", "--config", configFile)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if mock.capturedReq.ConfigText != configContent {
t.Errorf("config text mismatch: got %q, want %q", mock.capturedReq.ConfigText, configContent)
}
}

func TestPipelineRun_ParamsFileNotFound(t *testing.T) {
mock := &mockRunner{
response: &api.PipelineRunResponse{RunID: "run-1", RunName: "test"},
}
serverURL := "https://api.example.com"
accessToken := "token"
workspace := ""

cmd := buildCmd(&serverURL, &accessToken, &workspace, mock)
_, err := executeCmd(t, cmd, "run", "--pipeline", "my-pipe", "--params-file", "/nonexistent/params.yaml")
if err == nil {
t.Fatal("expected error for nonexistent params file")
}
if !strings.Contains(err.Error(), "failed to load params file") {
t.Errorf("unexpected error message: %v", err)
}
}
