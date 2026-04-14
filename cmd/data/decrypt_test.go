package data

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDataDecrypt_FileDefaultOutput(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "sample.txt")
	plaintext := []byte("hello world")
	if err := os.WriteFile(sourcePath, plaintext, 0600); err != nil {
		t.Fatal(err)
	}

	encryptCmd := newEncryptCmd()
	if _, err := executeDataCmd(encryptCmd, sourcePath, "--unsafe-local", "--crypt-password", "secret", "--crypt-salt", "pepper"); err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	encryptedPath := sourcePath + rcloneDefaultSuffix
	decryptedPath := defaultDecryptedPath(encryptedPath) + ".dec"

	decryptCmd := newDecryptCmd()
	out, err := executeDataCmd(decryptCmd, encryptedPath, "--unsafe-local", "--crypt-password", "secret", "--crypt-salt", "pepper")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("File decrypted successfully")) {
		t.Fatalf("expected output message, got %q", out)
	}

	decrypted, err := os.ReadFile(decryptedPath)
	if err != nil {
		t.Fatalf("read decrypted file: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted content mismatch")
	}
}

func TestDataDecrypt_RequiresPassword(t *testing.T) {
	dir := t.TempDir()
	encryptedPath := filepath.Join(dir, "sample.txt"+rcloneDefaultSuffix)
	if err := os.WriteFile(encryptedPath, []byte("not-real-encrypted-data"), 0600); err != nil {
		t.Fatal(err)
	}

	// Without --unsafe-local: should fail with managed-not-available error.
	cmd := newDecryptCmd()
	_, err := executeDataCmd(cmd, encryptedPath)
	if err == nil {
		t.Fatal("expected error when --unsafe-local is not set")
	}

	// With --unsafe-local but no password: should fail with missing-password error.
	cmd2 := newDecryptCmd()
	_, err2 := executeDataCmd(cmd2, encryptedPath, "--unsafe-local")
	if err2 == nil {
		t.Fatal("expected error for missing crypt-password in --unsafe-local mode")
	}
	if got := err2.Error(); got != "--crypt-password is required in --unsafe-local mode" {
		t.Fatalf("unexpected error: %v", err2)
	}
}
