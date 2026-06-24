package data

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/abccrypt"
)

// TestDataDecrypt_FileDefaultOutput: clean restoration to <name> (no .dec
// suffix). devon B1: decrypt should not produce ".dec".
func TestDataDecrypt_FileDefaultOutput(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeMinimalDataCLIConfig(t, cfgPath)
	t.Setenv("ABC_CLI_CONFIG_FILE", cfgPath)

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

	encryptedPath := sourcePath + abccrypt.Suffix

	// Remove the original so decrypt's clean target doesn't collide. The
	// no-collision case is the happy path the user sees most often.
	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("remove original: %v", err)
	}

	decryptCmd := newDecryptCmd()
	out, err := executeDataCmd(decryptCmd, encryptedPath, "--unsafe-local", "--crypt-password", "secret", "--crypt-salt", "pepper")
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("File decrypted successfully")) {
		t.Fatalf("expected success message, got %q", out)
	}

	// Devon B1: the clean restored name, not "<name>.dec".
	if _, err := os.Stat(sourcePath + ".dec"); err == nil {
		t.Fatalf("decrypt produced a .dec file at %q — should restore the original name without that suffix", sourcePath+".dec")
	}
	decrypted, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read decrypted file at clean path %q: %v", sourcePath, err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted content mismatch")
	}

	// Devon B2 (companion): the .encrypted source is preserved after decrypt.
	if _, err := os.Stat(encryptedPath); err != nil {
		t.Fatalf("decrypt removed the .encrypted source — it should be preserved")
	}
}

// TestDataDecrypt_RefusesToClobber: devon B1 + B2. When the clean restored
// target already exists, decrypt MUST refuse — no silent .dec suffix, no
// silent overwrite of the original.
func TestDataDecrypt_RefusesToClobber(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeMinimalDataCLIConfig(t, cfgPath)
	t.Setenv("ABC_CLI_CONFIG_FILE", cfgPath)

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
	encryptedPath := sourcePath + abccrypt.Suffix

	// sample.txt still exists (we did NOT remove it). Decrypting now must
	// refuse rather than overwrite or silently append ".dec".
	decryptCmd := newDecryptCmd()
	_, err := executeDataCmd(decryptCmd, encryptedPath, "--unsafe-local", "--crypt-password", "secret", "--crypt-salt", "pepper")
	if err == nil {
		t.Fatalf("expected decrypt to refuse clobbering existing %q", sourcePath)
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected refuse-clobber error, got: %v", err)
	}

	// No .dec was created either.
	if _, err := os.Stat(sourcePath + ".dec"); err == nil {
		t.Fatalf("decrypt produced a .dec file when refusing to overwrite — should produce neither")
	}

	// Original content untouched.
	got, _ := os.ReadFile(sourcePath)
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("original was modified despite refuse-clobber")
	}

	// --force opens the door (overwrites).
	decryptCmd2 := newDecryptCmd()
	if _, err := executeDataCmd(decryptCmd2, encryptedPath, "--unsafe-local", "--crypt-password", "secret", "--crypt-salt", "pepper", "--force"); err != nil {
		t.Fatalf("--force decrypt failed: %v", err)
	}
	got2, _ := os.ReadFile(sourcePath)
	if !bytes.Equal(got2, plaintext) {
		t.Fatalf("--force decrypt content mismatch (round trip broken)")
	}
}

// TestDataDecrypt_NoSuffixRequiresOutput: if the input has no .encrypted
// suffix, we cannot guess an output path — error rather than invent ".dec".
func TestDataDecrypt_NoSuffixRequiresOutput(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeMinimalDataCLIConfig(t, cfgPath)
	t.Setenv("ABC_CLI_CONFIG_FILE", cfgPath)

	dir := t.TempDir()
	// File without the rcloneDefaultSuffix on the name.
	noSuffix := filepath.Join(dir, "ciphertext.bin")
	if err := os.WriteFile(noSuffix, []byte("not-real-encrypted-data"), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := newDecryptCmd()
	_, err := executeDataCmd(cmd, noSuffix, "--unsafe-local", "--crypt-password", "secret", "--crypt-salt", "pepper")
	if err == nil {
		t.Fatal("expected error when input has no recognised crypt suffix")
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Fatalf("expected error pointing the user at --output, got: %v", err)
	}
}

func TestDataDecrypt_RequiresPassword(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeMinimalDataCLIConfig(t, cfgPath)
	t.Setenv("ABC_CLI_CONFIG_FILE", cfgPath)

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

func TestDefaultDecryptedPath(t *testing.T) {
	cases := []struct {
		in       string
		wantPath string
		wantOK   bool
	}{
		{"report.pdf" + abccrypt.Suffix, "report.pdf", true},
		{"/tmp/x/report.pdf" + abccrypt.Suffix, "/tmp/x/report.pdf", true},
		{"report.pdf", "", false},
		{"report.bin", "", false},               // wrong suffix
		{"report.pdf.encrypted", "", false},     // legacy rclone suffix no longer recognised
		{abccrypt.Suffix, "", false},            // suffix-only → empty trim
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := defaultDecryptedPath(c.in)
		if got != c.wantPath || ok != c.wantOK {
			t.Errorf("defaultDecryptedPath(%q) = (%q, %v), want (%q, %v)",
				c.in, got, ok, c.wantPath, c.wantOK)
		}
	}
}
