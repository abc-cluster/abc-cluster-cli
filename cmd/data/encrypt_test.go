package data

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// writeMinimalDataCLIConfig writes a config with one context so ContextForSecrets resolves.
func writeMinimalDataCLIConfig(t *testing.T, path string) {
	t.Helper()
	const contents = `version: "1"
active_context: test
contexts:
  test:
    endpoint: "http://localhost"
`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDataEncrypt_FileDefaultOutput(t *testing.T) {
	// Isolate config so stored crypt credentials don't bleed between tests.
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeMinimalDataCLIConfig(t, cfgPath)
	t.Setenv("ABC_CONFIG_FILE", cfgPath)

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "sample.txt")
	plaintext := []byte("hello world")
	if err := os.WriteFile(sourcePath, plaintext, 0600); err != nil {
		t.Fatal(err)
	}

	cmd := newEncryptCmd()
	out, err := executeDataCmd(cmd, sourcePath, "--unsafe-local", "--crypt-password", "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("File encrypted successfully")) {
		t.Fatalf("expected output message, got %q", out)
	}

	encryptedPath := sourcePath + rcloneDefaultSuffix
	t.Cleanup(func() {
		_ = os.Remove(encryptedPath)
	})
	encrypted, err := os.ReadFile(encryptedPath)
	if err != nil {
		t.Fatalf("read encrypted file: %v", err)
	}

	cryptor, err := newCryptConfig("secret", "", bytes.NewReader(make([]byte, rcloneFileNonceSize)))
	if err != nil {
		t.Fatalf("newCryptConfig: %v", err)
	}
	decrypted, err := decryptRclone(encrypted, cryptor.dataKey)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted content mismatch")
	}
}

func executeDataCmd(cmd *cobra.Command, args ...string) (string, error) {
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	_, err := cmd.ExecuteC()
	return buf.String(), err
}
