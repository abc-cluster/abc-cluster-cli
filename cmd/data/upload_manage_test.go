package data_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/data"
)

func TestDataUpload_StatusNoStateFound(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/1"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	out, err := executeCmd(t, cmd, "upload", tmpFile, "--status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No resume state found") {
		t.Fatalf("expected no-state message, got %q", out)
	}
	// Factory must not be called — no actual upload happens.
	if recorder.endpoint != "" {
		t.Fatal("factory should not be called for --status")
	}
}

func TestDataUpload_StatusShowsStoredURL(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	serverURL := "https://api.example.com"
	workspace := ""
	endpoint := "https://api.example.com/data/uploads/"
	const location = "https://api.example.com/data/uploads/files/abc123"

	statePath, err := data.UploadResumeStatePrimaryPathExported(endpoint, tmpFile)
	if err != nil {
		t.Fatalf("get state path: %v", err)
	}
	if err := data.StoreUploadResumeLocationExported(statePath, location); err != nil {
		t.Fatalf("store location: %v", err)
	}

	mock := &mockUploader{location: location}
	recorder := &factoryRecorder{uploader: mock}
	accessToken := "token"

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	out, err := executeCmd(t, cmd, "upload", tmpFile, "--status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, location) {
		t.Fatalf("expected location in output, got %q", out)
	}
	if !strings.Contains(out, endpoint) {
		t.Fatalf("expected endpoint in output, got %q", out)
	}
	if recorder.endpoint != "" {
		t.Fatal("factory should not be called for --status")
	}
}

func TestDataUpload_ClearRemovesStoredState(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	serverURL := "https://api.example.com"
	workspace := ""
	endpoint := "https://api.example.com/data/uploads/"
	const location = "https://api.example.com/data/uploads/files/todelete"

	statePath, err := data.UploadResumeStatePrimaryPathExported(endpoint, tmpFile)
	if err != nil {
		t.Fatalf("get state path: %v", err)
	}
	if err := data.StoreUploadResumeLocationExported(statePath, location); err != nil {
		t.Fatalf("store location: %v", err)
	}
	if _, statErr := os.Stat(statePath); statErr != nil {
		t.Fatalf("expected state file to exist: %v", statErr)
	}

	mock := &mockUploader{location: location}
	recorder := &factoryRecorder{uploader: mock}
	accessToken := "token"

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	out, err := executeCmd(t, cmd, "upload", tmpFile, "--clear")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "cleared") {
		t.Fatalf("expected cleared message, got %q", out)
	}
	if _, statErr := os.Stat(statePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected state file to be removed, stat err=%v", statErr)
	}
	if recorder.endpoint != "" {
		t.Fatal("factory should not be called for --clear")
	}
}

func TestDataUpload_ClearNoStateIsNotAnError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/1"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--clear")
	if err != nil {
		t.Fatalf("unexpected error clearing non-existent state: %v", err)
	}
}

func TestDataUpload_StatusAndClearMutuallyExclusive(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--status", "--clear")
	if err == nil {
		t.Fatal("expected error when both --status and --clear are set")
	}
	if !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("unexpected error: %v", err)
	}
}
