package data_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/api"
	"github.com/abc-cluster/abc-cluster-cli/cmd/data"
	"github.com/spf13/cobra"
)

type mockDownloadRunner struct {
	lastReq *api.PipelineRunRequest
	resp    *api.PipelineRunResponse
	err     error
}

func (m *mockDownloadRunner) SubmitPipelineRun(req *api.PipelineRunRequest) (*api.PipelineRunResponse, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	return m.resp, nil
}

func executeDataCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	_, err := cmd.ExecuteC()
	return buf.String(), err
}

func TestDataDownload_Submission(t *testing.T) {
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := "ws-1"

	runner := &mockDownloadRunner{resp: &api.PipelineRunResponse{RunID: "r-123", RunName: "fetchngs-run"}}
	oldPipelineFactory := data.PipelineFactory
	data.PipelineFactory = func(_, _, _ string) data.PipelineRunner { return runner }
	defer func() { data.PipelineFactory = oldPipelineFactory }()

	cmd := data.NewCmd(&serverURL, &accessToken, &workspace)
	out, err := executeDataCmd(t, cmd, "download", "--accession", "SRR000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.lastReq == nil {
		t.Fatal("expected pipeline run request to be sent")
	}
	if runner.lastReq.Pipeline != "https://github.com/nf-core/fetchngs" {
		t.Fatalf("unexpected pipeline URL: %q", runner.lastReq.Pipeline)
	}
	if requestAccession, ok := runner.lastReq.Params["accession"]; !ok || requestAccession != "SRR000000" {
		t.Fatalf("unexpected accession param %#v", runner.lastReq.Params)
	}
	if !strings.Contains(out, "Data download pipeline submitted successfully") {
		t.Fatalf("expected success output, got: %s", out)
	}
}

func TestDataDownload_ParamsFileLoad(t *testing.T) {
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := "ws-1"

	runner := &mockDownloadRunner{resp: &api.PipelineRunResponse{RunID: "r-456"}}
	oldPipelineFactory := data.PipelineFactory
	data.PipelineFactory = func(_, _, _ string) data.PipelineRunner { return runner }
	defer func() { data.PipelineFactory = oldPipelineFactory }()

	paramsFile := filepath.Join(t.TempDir(), "params.yml")
	os.WriteFile(paramsFile, []byte("accession: SRR000001\n"), 0600)

	cmd := data.NewCmd(&serverURL, &accessToken, &workspace)
	_, err := executeDataCmd(t, cmd, "download", "--params-file", paramsFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.lastReq == nil {
		t.Fatal("expected pipeline run request to be sent")
	}
	if runner.lastReq.Params["accession"] != "SRR000001" {
		t.Fatalf("unexpected accession param %#v", runner.lastReq.Params)
	}
}
