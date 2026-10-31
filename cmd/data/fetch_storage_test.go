package data

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// memberBroker serves a seedling/v1 /auth/exchange response the way the real
// broker does for a member slot: MinIO keys for slot-<name>, nothing on disk.
func memberBroker(t *testing.T, slot string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"whoami": slot,
			"source": "seedling/v1",
			"nomad": map[string]any{
				"addr":        "https://nomad.seedling.example.test",
				"token":       "nomad-token-from-broker",
				"namespace":   "su-test-group",
				"datacenters": []string{"seedling-prod"},
				"head_pool":   "platform",
				"worker_pool": "compute",
			},
			"minio": map[string]any{
				"endpoint":   "https://s3.seedling.example.test",
				"access_key": "slot-" + slot,
				"secret_key": "minio-secret-from-broker",
			},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ABC_AUTH_EXCHANGE_URL", srv.URL)
	t.Setenv("HOME", t.TempDir())
}

// memberContext has the shape a member's claim-code config produces: a broker
// cred_source, an opaque token, and no admin.services storage block. token
// must be unique per test, because resolvers are cached per token.
func memberContext(token, slot string) config.Context {
	ctx := config.Context{
		Endpoint:    "https://nomad.seedling.example.test",
		AccessToken: token,
		CredSource:  "seedling/v1",
	}
	ctx.SetAuthWhoami(slot)
	return ctx
}

func TestStorageDest_MemberContextGetsBrokerCreds(t *testing.T) {
	memberBroker(t, "ottovan")
	info, err := resolveStorageDestInfoFor(memberContext("abco_member-dest", "ottovan"),
		"s3://su-sdsct-ceri/users/ottovan/probe/")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if info.endpoint != "https://s3.seedling.example.test" {
		t.Errorf("endpoint = %q, want the broker's MinIO endpoint", info.endpoint)
	}
	if info.accessKey != "slot-ottovan" || info.secretKey != "minio-secret-from-broker" {
		t.Errorf("keys = %q/%q, want the broker's slot keys", info.accessKey, info.secretKey)
	}

	script, err := buildStorageScriptFor(&downloadOptions{tool: "wget", source: "https://example.org/a.fasta"}, info)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, want := range []string{
		"export AWS_ACCESS_KEY_ID='slot-ottovan'",
		"export AWS_SECRET_ACCESS_KEY='minio-secret-from-broker'",
		"s5cmd --endpoint-url 'https://s3.seedling.example.test'",
		"s3://su-sdsct-ceri/users/ottovan/probe/",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
}

// The upload must never be generated without keys: s5cmd would then probe the
// EC2 metadata endpoint for minutes, which is the "hang at [2/2]" in the report.
func TestStorageScript_NoCredentialsIsAnError(t *testing.T) {
	info := &storageDestInfo{endpoint: "https://s3.example.test", bucketPath: "s3://b/p/"}
	script, err := buildStorageScriptFor(&downloadOptions{tool: "wget", source: "https://example.org/a"}, info)
	if err == nil {
		t.Fatalf("expected an error, got a script:\n%s", script)
	}
	if !strings.Contains(err.Error(), "no S3 credentials") {
		t.Errorf("error = %v", err)
	}
}

func TestStorageDest_OperatorKeysStillWin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := config.Context{Endpoint: "https://nomad.example.test"}
	for field, v := range map[string]string{
		"endpoint":   "https://minio.operator.test",
		"access_key": "operator-key",
		"secret_key": "operator-secret",
	} {
		if err := config.SetAdminFloorField(&ctx.Admin.Services, "minio", field, v); err != nil {
			t.Fatal(err)
		}
	}
	info, err := resolveStorageDestInfoFor(ctx, "s3://b/p/")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if info.endpoint != "https://minio.operator.test" || info.accessKey != "operator-key" || info.secretKey != "operator-secret" {
		t.Errorf("got %+v", info)
	}
}

// useMemberConfig points config.Load at a config whose active context is a
// member, for the paths that load the config themselves.
func useMemberConfig(t *testing.T, token string) {
	t.Helper()
	memberBroker(t, "ottovan")
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &config.Config{
		ActiveContext:  "member",
		Contexts:       map[string]config.Context{"member": memberContext(token, "ottovan")},
		ContextAliases: map[string]string{},
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABC_CLI_CONFIG_FILE", path)
	t.Setenv("ABC_CLI_CONTEXT", "")
}

// --destination s3://… with wget used to produce `wget -P 's3://…'`, which
// writes into a local directory named s3: and exits 0 with nothing uploaded.
func TestToolScript_S3DestinationIsUploaded(t *testing.T) {
	useMemberConfig(t, "abco_member-s3dest")
	opts := &downloadOptions{tool: "wget", source: "https://example.org/AHFZ01.fasta",
		destination: "s3://su-sdsct-ceri/users/ottovan/probe2/"}
	script, err := buildToolScript(opts, "", "", "", "", "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(script, "-P 's3://") {
		t.Errorf("wget was pointed at the s3:// URI as a directory:\n%s", script)
	}
	for _, want := range []string{
		"wget -c -P '/tmp/abc-data-dl-tmp'",
		"export AWS_ACCESS_KEY_ID='slot-ottovan'",
		"cp '/tmp/abc-data-dl-tmp/*' s3://su-sdsct-ceri/users/ottovan/probe2/",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
}

func TestToolScript_S3DestinationAndStoragePathConflict(t *testing.T) {
	opts := &downloadOptions{tool: "wget", source: "https://example.org/a",
		destination: "s3://b/one/", storagePath: "s3://b/two/"}
	if _, err := buildToolScript(opts, "", "", "", "", ""); err == nil {
		t.Fatal("expected an error when both name an upload target")
	}
}

func TestToolScript_RejectsSchemelessDestination(t *testing.T) {
	opts := &downloadOptions{tool: "wget", source: "https://example.org/a", destination: "bucket/prefix"}
	_, err := buildToolScript(opts, "", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported --destination") {
		t.Fatalf("err = %v", err)
	}
}

// The --url-file path is on the submitting machine; the job used to `cat` it
// on the node, where it does not exist.
func TestToolScript_URLFileIsEmbedded(t *testing.T) {
	list := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(list, []byte("https://example.org/a.fq.gz\r\n\n# comment\nhttps://example.org/b.fq.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := &downloadOptions{tool: "wget", urlFile: list, destination: "/scratch/in"}
	script, err := buildToolScript(opts, "", "", "", "", "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(script, list) {
		t.Errorf("script still references the local path %s", list)
	}
	want := "cat > " + embeddedURLFilePath + " <<'ABC_URL_LIST_EOF'\nhttps://example.org/a.fq.gz\nhttps://example.org/b.fq.gz\nABC_URL_LIST_EOF\n"
	if !strings.Contains(script, want) {
		t.Errorf("embedded list missing; script:\n%s", script)
	}
	if !strings.Contains(script, "cat '"+embeddedURLFilePath+"' | xargs") {
		t.Errorf("wget does not read the embedded copy:\n%s", script)
	}
	if opts.urlFile != list {
		t.Errorf("caller's opts were modified: urlFile = %q", opts.urlFile)
	}
}

func TestToolScript_MissingURLFileIsAnError(t *testing.T) {
	opts := &downloadOptions{tool: "wget", urlFile: filepath.Join(t.TempDir(), "absent.txt"), destination: "/scratch/in"}
	_, err := buildToolScript(opts, "", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "--url-file") {
		t.Fatalf("err = %v", err)
	}
}

func TestToolPreflight_Aria2ChecksAria2c(t *testing.T) {
	got := toolPreflight("aria2", "s5cmd", "aria2")
	if !strings.Contains(got, "command -v aria2c ") || !strings.Contains(got, "command -v s5cmd ") {
		t.Errorf("preflight:\n%s", got)
	}
	if strings.Count(got, "command -v aria2c ") != 1 {
		t.Errorf("aria2c checked more than once:\n%s", got)
	}
}

// s5cmd cp has no --from-file flag; a URL list goes through `s5cmd run`.
func TestToolCommand_S5cmdURLListUsesRun(t *testing.T) {
	cmd, err := buildToolCommand(&downloadOptions{tool: "s5cmd", urlFile: "/tmp/l.txt", destination: "/scratch/in",
		toolArgs: "--no-sign-request", parallel: 8})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cmd, "--from-file") {
		t.Errorf("uses the nonexistent --from-file: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "| s5cmd --no-sign-request --numworkers 8 run") {
		t.Errorf("cmd = %s", cmd)
	}
}

func captureSubmit(t *testing.T) (*dataNomadScriptOpts, *string) {
	t.Helper()
	var gotOpts dataNomadScriptOpts
	var gotBody string
	prev := submitDataNomadScriptFn
	submitDataNomadScriptFn = func(_ *cobra.Command, o dataNomadScriptOpts, body string) error {
		gotOpts, gotBody = o, body
		return nil
	}
	t.Cleanup(func() { submitDataNomadScriptFn = prev })
	return &gotOpts, &gotBody
}

// A member's data fetch runs on exec with the abc-tools volume (ADR-0061)
// rather than staging tools as anonymous artifacts, and passes --node-pool.
func TestRunFetch_MemberUploadUsesAbcToolsOnExec(t *testing.T) {
	useMemberConfig(t, "abco_member-runfetch")
	gotOpts, gotBody := captureSubmit(t)

	cmd := newFetchCmd(new(string), new(string), new(string))
	cmd.SetArgs([]string{"https://example.org/AHFZ01.fasta", "--tool", "s5cmd",
		"--storage-path", "s3://su-sdsct-ceri/users/ottovan/probe/", "--node-pool", "platform"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if gotOpts.Driver != "exec" {
		t.Errorf("driver = %q, want exec (raw_exec is refused for members)", gotOpts.Driver)
	}
	if !gotOpts.Tools {
		t.Error("abc-tools volume not requested")
	}
	if len(gotOpts.Artifacts) != 0 {
		t.Errorf("artifacts = %+v, want none", gotOpts.Artifacts)
	}
	if !strings.HasPrefix(*gotBody, "export PATH=\""+abcToolsBinDir+":${PATH}\"\n") {
		t.Errorf("abc-tools bin dir not first on PATH:\n%s", *gotBody)
	}
	if !strings.Contains(*gotBody, "export AWS_ACCESS_KEY_ID='slot-ottovan'") {
		t.Errorf("upload has no member credentials:\n%s", *gotBody)
	}

	argv := dataJobRunArgv(*gotOpts, "img", "/tmp/s.sh")
	joined := strings.Join(argv, " ")
	for _, want := range []string{"--driver exec", "--node-pool platform", "--tools"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q: %s", want, joined)
		}
	}
	if argv[len(argv)-1] != "/tmp/s.sh" {
		t.Errorf("script path is not the last argument: %s", joined)
	}
}
