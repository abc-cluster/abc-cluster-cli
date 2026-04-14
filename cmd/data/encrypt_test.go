package data

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestDataEncrypt_FileDefaultOutput(t *testing.T) {
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
