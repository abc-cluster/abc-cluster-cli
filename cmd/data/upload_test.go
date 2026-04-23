package data_test

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/data"
	"github.com/spf13/cobra"
)

type mockUploader struct {
	mu            sync.Mutex
	calls         []uploadCall
	location      string
	err           error
	preflightErr  error
	preflightRuns int
}

type uploadCall struct {
	filePath string
	metadata map[string]string
}

func (m *mockUploader) Upload(_ context.Context, filePath string, metadata map[string]string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, uploadCall{filePath: filePath, metadata: metadata})
	return m.location, m.err
}

func (m *mockUploader) PreflightNetwork(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.preflightRuns++
	return m.preflightErr
}

type factoryRecorder struct {
	uploader    *mockUploader
	endpoint    string
	accessToken string
	opts        data.UploaderOptions
	err         error
}

func (f *factoryRecorder) factory(endpoint, accessToken string, opts data.UploaderOptions) (data.Uploader, error) {
	f.endpoint = endpoint
	f.accessToken = accessToken
	f.opts = opts
	return f.uploader, f.err
}

func setTestConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ABC_CONFIG_FILE", filepath.Join(t.TempDir(), "config.yaml"))
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

func buildCmd(t *testing.T, serverURL, accessToken, workspace *string, factory data.ClientFactory) *cobra.Command {
	setTestConfigEnv(t)
	return data.NewCmd(serverURL, accessToken, workspace, factory)
}

func TestDataUpload_Basic(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")

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
	out, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "https://api.example.com/files/" {
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
	if !strings.Contains(out, "Size: ") || !strings.Contains(out, "bytes") {
		t.Errorf("expected size in bytes output for tiny file, got %q", out)
	}
	if !strings.Contains(out, "Checksum: sha256:") {
		t.Errorf("expected checksum output, got %q", out)
	}
	if strings.Contains(out, mock.location) {
		t.Errorf("did not expect location in output, got %q", out)
	}
	if strings.Contains(out, "Location:") {
		t.Errorf("did not expect location label in output, got %q", out)
	}
}

func TestDataUpload_UsesAccessTokenByDefault(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/default-token"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "api-token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.accessToken != "api-token" {
		t.Fatalf("expected access token fallback, got %q", recorder.accessToken)
	}
}

func TestDataUpload_UsesNomadEnvTokenBeforeAccessToken(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")
	t.Setenv("ABC_TOKEN", "")
	t.Setenv("NOMAD_TOKEN", "nomad-env-token")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/default-token"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "api-token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.accessToken != "nomad-env-token" {
		t.Fatalf("expected NOMAD_TOKEN fallback, got %q", recorder.accessToken)
	}
}

func TestDataUpload_UsesContextNomadTokenBeforeAccessToken(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")
	t.Setenv("ABC_TOKEN", "")
	t.Setenv("NOMAD_TOKEN", "")
	setTestConfigEnv(t)
	cfgPath := os.Getenv("ABC_CONFIG_FILE")
	yaml := `version: "1"
active_context: dev
contexts:
  dev:
    endpoint: https://api.example.com
    access_token: api-token-in-context
    admin:
      services:
        nomad:
          nomad_token: nomad-token-from-context
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/default-token"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "api-token-cli-fallback"
	workspace := ""

	cmd := data.NewCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.accessToken != "nomad-token-from-context" {
		t.Fatalf("expected context nomad token fallback, got %q", recorder.accessToken)
	}
}

func TestDataUpload_UploadTokenOverridesAccessToken(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/upload-token"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "api-token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--upload-token", "tusd-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.accessToken != "tusd-token" {
		t.Fatalf("expected upload token override, got %q", recorder.accessToken)
	}
}

func TestDataUpload_EndpointDerivedFromContextAPIEndpoint(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")
	setTestConfigEnv(t)
	cfgPath := os.Getenv("ABC_CONFIG_FILE")
	yaml := `version: "1"
active_context: x
contexts:
  x:
    endpoint: https://api.example.com/corp/v2
    access_token: api-tok
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	mock := &mockUploader{location: "https://api.example.com/corp/v2/files/1"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://other.example.com"
	accessToken := "cli-token"
	workspace := ""
	cmd := data.NewCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "https://api.example.com/corp/v2/files/" {
		t.Fatalf("endpoint from derived context API URL: got %q", recorder.endpoint)
	}
}

func TestDataUpload_EndpointAndTokenFromContext(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")
	// Do not use buildCmd here: it calls setTestConfigEnv again and would move ABC_CONFIG_FILE
	// to a fresh empty config after we write our test YAML.
	setTestConfigEnv(t)
	cfgPath := os.Getenv("ABC_CONFIG_FILE")
	yaml := `version: "1"
active_context: dev
contexts:
  dev:
    endpoint: https://api.example.com
    upload_endpoint: https://uploads.test/files/
    upload_token: tus-from-context
    access_token: api-token-in-context
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.test/files/1"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "cli-access-token-fallback"
	workspace := "ws-1"

	cmd := data.NewCmd(&serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "https://uploads.test/files/" {
		t.Fatalf("endpoint from context: got %q", recorder.endpoint)
	}
	if recorder.accessToken != "tus-from-context" {
		t.Fatalf("tus bearer from context upload_token: got %q", recorder.accessToken)
	}
}

func TestDataUpload_WithWorkspace(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")

	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/2"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com/v1"
	accessToken := "token"
	workspace := "ws-1"

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed, err := url.Parse(recorder.endpoint)
	if err != nil {
		t.Fatalf("invalid endpoint: %v", err)
	}
	if parsed.Path != "/v1/files/" {
		t.Fatalf("unexpected endpoint path: %s", parsed.Path)
	}
	if parsed.RawQuery != "" {
		t.Fatalf("unexpected query string: %s", parsed.RawQuery)
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

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--endpoint", endpoint)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != endpoint {
		t.Fatalf("expected endpoint %q, got %q", endpoint, recorder.endpoint)
	}

	parsed, err := url.Parse(recorder.endpoint)
	if err != nil {
		t.Fatalf("invalid endpoint: %v", err)
	}
	if parsed.Path != "/files" {
		t.Fatalf("unexpected endpoint path: %s", parsed.Path)
	}
	if parsed.Query().Get("region") != "west" {
		t.Fatalf("expected region query preserved: %s", parsed.RawQuery)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 upload call, got %d", len(mock.calls))
	}
}

func TestDataUpload_EndpointFromEnvironment(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "https://uploads.example.com/files?region=dev")

	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/3"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := "ws-2"

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != os.Getenv("ABC_UPLOAD_ENDPOINT") {
		t.Fatalf("expected endpoint %q, got %q", os.Getenv("ABC_UPLOAD_ENDPOINT"), recorder.endpoint)
	}

	parsed, err := url.Parse(recorder.endpoint)
	if err != nil {
		t.Fatalf("invalid endpoint: %v", err)
	}
	if parsed.Path != "/files" {
		t.Fatalf("unexpected endpoint path: %s", parsed.Path)
	}
	if parsed.Query().Get("region") != "dev" {
		t.Fatalf("expected region query preserved: %s", parsed.RawQuery)
	}
}

func TestDataUpload_CustomEndpointIgnoresWorkspace(t *testing.T) {
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

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--endpoint", endpoint)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != endpoint {
		t.Fatalf("expected endpoint %q, got %q", endpoint, recorder.endpoint)
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

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
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
	sort.Strings(relativePaths)
	expected := []string{"a.txt", "b.txt", filepath.ToSlash(filepath.Join("nested", "c.txt"))}
	sort.Strings(expected)
	for i, rel := range expected {
		if relativePaths[i] != rel {
			t.Fatalf("expected relative path %q, got %q", rel, relativePaths[i])
		}
	}
	if !strings.Contains(out, "Uploading 3 files") {
		t.Fatalf("expected upload summary, got %q", out)
	}
	if !strings.Contains(out, "Size: ") || !strings.Contains(out, "bytes") {
		t.Fatalf("expected size in bytes output for tiny file, got %q", out)
	}
	if !strings.Contains(out, "Checksum: sha256:") {
		t.Fatalf("expected checksum output for directory upload, got %q", out)
	}
}

func TestDataUpload_DirectoryUploadsFilesWithoutParallel(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	for _, path := range []string{fileA, fileB} {
		if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/parallel-off"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	out, err := executeCmd(t, cmd, "upload", dir, "--parallel=false")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 upload calls, got %d", len(mock.calls))
	}
	if !strings.Contains(out, "Uploading 2 files") {
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

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
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

func TestDataUpload_ChecksumDisabled(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/checksum-off"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	out, err := executeCmd(t, cmd, "upload", tmpFile, "--checksum=false")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 upload call, got %d", len(mock.calls))
	}
	if v, ok := mock.calls[0].metadata["checksum"]; !ok || v != "" {
		t.Fatalf("expected checksum metadata marker to disable checksum, got %q (present=%v)", v, ok)
	}
	if strings.Contains(out, "Checksum:") {
		t.Fatalf("did not expect checksum output when disabled, got %q", out)
	}
}

func TestDataUpload_MissingFile(t *testing.T) {
	mock := &mockUploader{location: "https://uploads.example.com/files/5"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", "/missing/file")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "input error") || !strings.Contains(err.Error(), "does not exist") {
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

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", dir, "--name", "nope")
	if err == nil {
		t.Fatal("expected error for directory upload with name")
	}
	if !strings.Contains(err.Error(), "input error") || !strings.Contains(err.Error(), "--name can only be used") {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "" {
		t.Fatalf("factory should not be called on invalid args")
	}
}

func TestDataUpload_InvalidParallelJobs(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/invalid-jobs"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", dir, "--parallel-jobs", "0")
	if err == nil {
		t.Fatal("expected error for invalid parallel job count")
	}
	if !strings.Contains(err.Error(), "input error") || !strings.Contains(err.Error(), "--parallel-jobs must be >= 1") {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.endpoint != "" {
		t.Fatalf("factory should not be called on invalid args")
	}
}

func TestDataUpload_PreflightNetworkError(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(tmpFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{
		preflightErr: fmt.Errorf("cannot resolve upload host \"dev.abc-cluster.cloud\" from endpoint \"http://dev.abc-cluster.cloud/files/\""),
	}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err == nil {
		t.Fatal("expected preflight network error")
	}
	if !strings.Contains(err.Error(), "network/server error") || !strings.Contains(err.Error(), "pre-flight network check failed") {
		t.Fatalf("unexpected preflight error prefix: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot resolve upload host") {
		t.Fatalf("expected informative DNS error details, got: %v", err)
	}
	if len(mock.calls) != 0 {
		t.Fatalf("upload should not start when preflight fails, got %d upload calls", len(mock.calls))
	}
	if mock.preflightRuns != 1 {
		t.Fatalf("expected exactly one preflight run, got %d", mock.preflightRuns)
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

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile)
	if err == nil {
		t.Fatal("expected upload error")
	}
	if !strings.Contains(err.Error(), "network/server error") || !strings.Contains(err.Error(), "data upload failed") {
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

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
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

func TestDataUpload_ChunkSizeFlagPropagated(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/cs"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--chunk-size", "4MB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.opts.ChunkSize != 4_000_000 {
		t.Fatalf("expected ChunkSize=4000000, got %d", recorder.opts.ChunkSize)
	}
}

func TestDataUpload_MaxRateFlagPropagated(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/mr"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--max-rate", "10MB/s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.opts.MaxRate != 10_000_000 {
		t.Fatalf("expected MaxRate=10000000, got %d", recorder.opts.MaxRate)
	}
}

func TestDataUpload_MetaFlagMergedIntoMetadata(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/meta"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--meta", "project=myproject", "--meta", "owner=alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 upload call, got %d", len(mock.calls))
	}
	if mock.calls[0].metadata["project"] != "myproject" {
		t.Fatalf("expected project metadata, got %q", mock.calls[0].metadata["project"])
	}
	if mock.calls[0].metadata["owner"] != "alice" {
		t.Fatalf("expected owner metadata, got %q", mock.calls[0].metadata["owner"])
	}
	// Built-in key must not be overridden by --meta.
	if mock.calls[0].metadata["filename"] != "sample.txt" {
		t.Fatalf("expected filename to be sample.txt, got %q", mock.calls[0].metadata["filename"])
	}
}

func TestDataUpload_MetaFlagInvalidFormat(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/meta-bad"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--meta", "badformat")
	if err == nil {
		t.Fatal("expected error for invalid meta format")
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = recorder
}

func TestDataUpload_NoResumeFlagPropagated(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/nr"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--no-resume")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !recorder.opts.NoResume {
		t.Fatal("expected NoResume=true in factory opts")
	}
}

func TestDataUpload_ChunkSizeInvalidFormat(t *testing.T) {
	t.Setenv("ABC_UPLOAD_ENDPOINT", "")
	t.Setenv("ABC_UPLOAD_TOKEN", "")

	tmpFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	mock := &mockUploader{location: "https://uploads.example.com/files/cs-bad"}
	recorder := &factoryRecorder{uploader: mock}
	serverURL := "https://api.example.com"
	accessToken := "token"
	workspace := ""

	cmd := buildCmd(t, &serverURL, &accessToken, &workspace, recorder.factory)
	_, err := executeCmd(t, cmd, "upload", tmpFile, "--chunk-size", "notabytes")
	if err == nil {
		t.Fatal("expected error for invalid chunk-size")
	}
	if !strings.Contains(err.Error(), "chunk-size") {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = recorder
}
