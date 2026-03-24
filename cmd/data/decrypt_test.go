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
	if _, err := executeDataCmd(encryptCmd, sourcePath, "--crypt-password", "secret", "--crypt-salt", "pepper"); err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	encryptedPath := sourcePath + rcloneDefaultSuffix
	decryptedPath := defaultDecryptedPath(encryptedPath) + ".dec"

	decryptCmd := newDecryptCmd()
	out, err := executeDataCmd(decryptCmd, encryptedPath, "--crypt-password", "secret", "--crypt-salt", "pepper")
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

	cmd := newDecryptCmd()
	_, err := executeDataCmd(cmd, encryptedPath)
	if err == nil {
		t.Fatal("expected error for missing crypt-password")
	}
	if got := err.Error(); got != "crypt-password is required for decryption" {
		t.Fatalf("unexpected error: %v", err)
	}
}
