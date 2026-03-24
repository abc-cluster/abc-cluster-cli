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
	calls    []uploadCall
	location string
	err      error
}

type uploadCall struct {
	filePath string
	metadata map[string]string
}

func (m *mockUploader) Upload(_ context.Context, filePath string, metadata map[string]string) (string, error) {
	m.calls = append(m.calls, uploadCall{filePath: filePath, metadata: metadata})
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
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 upload call, got %d", len(mock.calls))
	}
	if mock.calls[0].filePath != tmpFile {
		t.Errorf("expected file path %q, got %q", tmpFile, mock.calls[0].filePath)
	}
	if mock.calls[0].metadata["filename"] != "sample.txt" {
		t.Errorf("expected filename metadata, got %q", mock.calls[0].metadata["filename"])
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
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 upload call, got %d", len(mock.calls))
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
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 upload call, got %d", len(mock.calls))
	}
}

func TestDataUpload_WorkspaceConflict(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/6"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := "ws-2"
	endpoint := "https://uploads.example.com/files?workspaceId=ws-1"

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--endpoint", endpoint)
	if err == nil {
		t.Fatal("expected workspace conflict error")
	}
	if !strings.Contains(err.Error(), "workspaceId") {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "" {
		t.Fatalf("factory should not be called on workspace conflict")
	}
}

func TestDataUpload_DirectoryUploadsFiles(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	subdir := filepath.Join(dir, "nested")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	fileC := filepath.Join(subdir, "c.txt")
	for _, path := range []string{fileA, fileB, fileC} {
		if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/7"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	out, err := executeCmd(t, cmd, "upload", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.calls) != 3 {
		t.Fatalf("expected 3 upload calls, got %d", len(mock.calls))
	}
	relativePaths := []string{
		mock.calls[0].metadata["relativePath"],
		mock.calls[1].metadata["relativePath"],
		mock.calls[2].metadata["relativePath"],
	}
	expected := []string{"a.txt", "b.txt", filepath.ToSlash(filepath.Join("nested", "c.txt"))}
	for i, rel := range expected {
		if relativePaths[i] != rel {
			t.Fatalf("expected relative path %q, got %q", rel, relativePaths[i])
		}
	}
	if !strings.Contains(out, "Uploading 3 files") {
		t.Fatalf("expected upload summary, got %q", out)
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
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 upload call, got %d", len(mock.calls))
	}
	if mock.calls[0].metadata["name"] != "project-data" {
		t.Fatalf("expected name metadata, got %q", mock.calls[0].metadata["name"])
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
	if !strings.Contains(err.Error(), "failed to access path") {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "" {
		t.Fatalf("factory should not be called on missing file")
	}
}

func TestDataUpload_DirectoryWithName(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(fileA, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/8"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", dir, "--name", "nope")
	if err == nil {
		t.Fatal("expected error for directory upload with name")
	}
	if !strings.Contains(err.Error(), "--name can only be used") {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "" {
		t.Fatalf("factory should not be called on invalid args")
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

func TestDataUpload_CryptSaltWithoutPassword(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/9"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--crypt-salt", "pepper")
	if err == nil {
		t.Fatal("expected error for missing crypt password")
	}
	if !strings.Contains(err.Error(), "crypt-password") {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "" {
		t.Fatalf("factory should not be called on invalid encryption args")
	}
}
