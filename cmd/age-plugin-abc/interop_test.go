package main_test

// Interop proof for ADR-0067 managed mode: a file is a file, whichever tool
// wrote it. Drives the REAL stock `age` binary + the built age-plugin-abc +
// a fake broker, and checks byte-identical plaintext both directions:
//
//	age -e -R recipients.txt  →  age -d -j abc            (age round-trip via plugin)
//	abccrypt.Encrypt (abc)    →  age -d -j abc            (abc-written file opens in stock age)
//
// Skips when the `age` binary isn't on PATH (so it stays CI-portable).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age/plugin"

	"github.com/abc-cluster/abc-cluster-cli/internal/abccrypt"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/keysource"
)

const (
	testGroupKekID = "group:demo"
)

func fixedKEK() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

// fakeBroker serves /auth/keys/get → group:demo's KEK at v1.
func fakeBroker(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kek_id": testGroupKekID, "version": 1,
			"kek": base64.StdEncoding.EncodeToString(fixedKEK()),
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildPlugin builds age-plugin-abc into dir and returns the dir (for PATH).
func buildPlugin(t *testing.T, dir string) {
	t.Helper()
	out := filepath.Join(dir, "age-plugin-abc")
	cmd := exec.Command("go", "build", "-o", out, ".")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build age-plugin-abc: %v\n%s", err, b)
	}
}

// writeConfig writes a minimal ~/.abc/config.yaml with an active context that
// carries an opaque token, and returns its path.
func writeConfig(t *testing.T, dir, brokerURL string) string {
	t.Helper()
	cfg := &abccfg.Config{ActiveContext: "test"}
	cfg.Contexts = map[string]abccfg.Context{
		"test": {
			Endpoint:     "https://aither.example/api",
			AccessToken:  "opaque-test-token",
			AuthEndpoint: brokerURL,
			CredSource:   "seedling/v1",
		},
	}
	path := filepath.Join(dir, "config.yaml")
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// ageEnv returns the env for an `age` subprocess: plugin on PATH, config + broker
// pointed at our fakes.
func ageEnv(pluginDir, cfgPath, brokerURL string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"PATH="+pluginDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"ABC_CLI_CONFIG_FILE="+cfgPath,
		"ABC_KEYS_BROKER_URL="+brokerURL,
	)
	return env
}

func TestInteropAgeRoundTripAndAbcToAge(t *testing.T) {
	agePath, err := exec.LookPath("age")
	if err != nil {
		t.Skip("stock `age` binary not on PATH; skipping interop test")
	}
	dir := t.TempDir()
	buildPlugin(t, dir)
	broker := fakeBroker(t)
	cfgPath := writeConfig(t, dir, broker.URL)
	env := ageEnv(dir, cfgPath, broker.URL)

	plaintext := []byte("the quick brown fox — managed age interop\n")

	// --- Direction 1: age -e -R recipients.txt  →  age -d -j abc ---
	rcptFile := filepath.Join(dir, "recipients.txt")
	rcpt := plugin.EncodeRecipient("abc", []byte(testGroupKekID))
	if err := os.WriteFile(rcptFile, []byte(rcpt+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ageFile := filepath.Join(dir, "viaage.age")

	enc := exec.Command(agePath, "-e", "-R", rcptFile, "-o", ageFile)
	enc.Stdin = bytes.NewReader(plaintext)
	enc.Env = env
	if b, err := enc.CombinedOutput(); err != nil {
		t.Fatalf("age encrypt: %v\n%s", err, b)
	}
	if got := ageDecryptViaPlugin(t, agePath, env, ageFile); !bytes.Equal(got, plaintext) {
		t.Fatalf("age round-trip mismatch:\n got %q\nwant %q", got, plaintext)
	}

	// --- Direction 2: abc-written file (abccrypt) opens in stock age -d -j abc ---
	t.Setenv("ABC_KEYS_BROKER_URL", broker.URL)
	cl, err := keysource.NewClient(abccfg.Context{AccessToken: "opaque-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	prov := keysource.NewProvider(context.Background(), cl)
	rcptObj, err := abccrypt.NewABCRecipient(testGroupKekID, prov)
	if err != nil {
		t.Fatal(err)
	}
	abcFile := filepath.Join(dir, "viaabc.age")
	out, err := os.Create(abcFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := abccrypt.Encrypt(out, bytes.NewReader(plaintext), rcptObj); err != nil {
		t.Fatalf("abccrypt.Encrypt: %v", err)
	}
	_ = out.Close()
	if got := ageDecryptViaPlugin(t, agePath, env, abcFile); !bytes.Equal(got, plaintext) {
		t.Fatalf("abc→age mismatch:\n got %q\nwant %q", got, plaintext)
	}
}

func ageDecryptViaPlugin(t *testing.T, agePath string, env []string, file string) []byte {
	t.Helper()
	dec := exec.Command(agePath, "-d", "-j", "abc", file)
	dec.Env = env
	var stdout, stderr bytes.Buffer
	dec.Stdout = &stdout
	dec.Stderr = &stderr
	if err := dec.Run(); err != nil {
		t.Fatalf("age decrypt -j abc %s: %v\n%s", filepath.Base(file), err, stderr.String())
	}
	return stdout.Bytes()
}
