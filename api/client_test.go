package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/api"
)

func TestSubmitPipelineRun_Success(t *testing.T) {
	want := &api.PipelineRunResponse{
		RunID:   "run-123",
		RunName: "my-run",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/pipelines/run") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected Authorization header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		}

		var req api.PipelineRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if req.Pipeline != "my-pipeline" {
			t.Errorf("expected pipeline 'my-pipeline', got %s", req.Pipeline)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(want); err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-token", "")
	resp, err := client.SubmitPipelineRun(&api.PipelineRunRequest{
		Pipeline: "my-pipeline",
		RunName:  "my-run",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.RunID != want.RunID {
		t.Errorf("expected RunID %q, got %q", want.RunID, resp.RunID)
	}
	if resp.RunName != want.RunName {
		t.Errorf("expected RunName %q, got %q", want.RunName, resp.RunName)
	}
}

func TestSubmitPipelineRun_WithWorkspace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("workspaceId") != "ws-456" {
			t.Errorf("expected workspaceId=ws-456, got %s", r.URL.Query().Get("workspaceId"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"runId":"run-1","runName":"test"}`)
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", "ws-456")
	_, err := client.SubmitPipelineRun(&api.PipelineRunRequest{Pipeline: "p"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubmitPipelineRun_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"invalid pipeline"}`)
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "", "")
	_, err := client.SubmitPipelineRun(&api.PipelineRunRequest{Pipeline: "bad"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("expected HTTP 400 in error, got: %v", err)
	}
}

func TestSubmitPipelineRun_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `not-json`)
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "", "")
	_, err := client.SubmitPipelineRun(&api.PipelineRunRequest{Pipeline: "p"})
	if err == nil {
		t.Fatal("expected a parse error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse response") {
		t.Errorf("expected 'failed to parse response' in error, got: %v", err)
	}
}

func TestNewClient_DefaultsSet(t *testing.T) {
	c := api.NewClient("https://example.com", "mytoken", "ws-1")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}
