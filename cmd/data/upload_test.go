package data_test

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/data"
	"github.com/spf13/cobra"
)

type mockUploader struct {
	filePath string
	metadata map[string]string
	location string
	err      error
}

func (m *mockUploader) Upload(_ context.Context, filePath string, metadata map[string]string) (string, error) {
	m.filePath = filePath
	m.metadata = metadata
	return m.location, m.err
}

type factoryRecorder struct {
	uploader    *mockUploader
	endpoint    string
	accessToken string
	err         error
}

func (f *factoryRecorder) factory(endpoint, accessToken string) (data.Uploader, error) {
	f.endpoint = endpoint
	f.accessToken = accessToken
	return f.uploader, f.err
}

func executeCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	_, err := cmd.ExecuteC()
	return buf.String(), err
}

func buildCmd(serverURL, accessToken, workspace *string, factory data.ClientFactory) *cobra.Command {
	return data.NewCmd(serverURL, accessToken, workspace, factory)
}

func TestDataUpload_Basic(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/1"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	out, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "https://api.example.com/data/uploads" {
		t.Fatalf("unexpected endpoint: %s", recorder.endpoint)
	}
	if recorder.accessToken != accessToken {
		t.Fatalf("expected access token to be forwarded")
	}
	if mock.filePath != tmpFile {
		t.Errorf("expected file path %q, got %q", tmpFile, mock.filePath)
	}
	if mock.metadata["filename"] != "sample.txt" {
		t.Errorf("expected filename metadata, got %q", mock.metadata["filename"])
	}
	if !strings.Contains(out, "File uploaded successfully") {
		t.Errorf("expected success output, got %q", out)
	}
	if !strings.Contains(out, mock.location) {
		t.Errorf("expected location in output, got %q", out)
	}
}

func TestDataUpload_WithWorkspace(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/2"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com/v1"
	accessToken := "token"
	workspace := "ws-1"

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed, err := url.Parse(recorder.endpoint)
	if err != nil {
		t.Fatalf("invalid endpoint: %v", err)
	}
	if parsed.Path != "/v1/data/uploads" {
		t.Fatalf("unexpected endpoint path: %s", parsed.Path)
	}
	if parsed.Query().Get("workspaceId") != "ws-1" {
		t.Fatalf("workspaceId missing in query: %s", parsed.RawQuery)
	}
}

func TestDataUpload_CustomEndpoint(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/3"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := "ws-2"
	endpoint := "https://uploads.example.com/files?region=west"

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--endpoint", endpoint)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed, err := url.Parse(recorder.endpoint)
	if err != nil {
		t.Fatalf("invalid endpoint: %v", err)
	}
	if parsed.Path != "/files" {
		t.Fatalf("unexpected endpoint path: %s", parsed.Path)
	}
	if parsed.Query().Get("workspaceId") != "ws-2" {
		t.Fatalf("workspaceId missing in query: %s", parsed.RawQuery)
	}
	if parsed.Query().Get("region") != "west" {
		t.Fatalf("expected region query preserved: %s", parsed.RawQuery)
	}
}

func TestDataUpload_WithName(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/4"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--name", "project-data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.metadata["name"] != "project-data" {
		t.Fatalf("expected name metadata, got %q", mock.metadata["name"])
	}
}

func TestDataUpload_MissingFile(t *testing.T) {
	mock := &mockUploader{location: "https://uploads.example.com/files/5"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", "/missing/file")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "failed to access file") {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "" {
		t.Fatalf("factory should not be called on missing file")
	}
}

func TestDataUpload_UploaderError(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{err: fmt.Errorf("boom")}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err == nil {
		t.Fatal("expected upload error")
	}
	if !strings.Contains(err.Error(), "data upload failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
