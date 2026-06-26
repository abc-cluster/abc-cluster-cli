package data

// Interop proof for ADR-0067 Amendment 2: managed files are NATIVE age X25519,
// so stock `age` reads/writes them with NO plugin. Drives the real `age` binary
// against an abc-written file and vice-versa, byte-identical both directions.
// Skips when `age` isn't on PATH.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/abc-cluster/abc-cluster-cli/internal/abccrypt"
)

func TestNativeAgeInteropNoPlugin(t *testing.T) {
	agePath, err := exec.LookPath("age")
	if err != nil {
		t.Skip("stock `age` binary not on PATH; skipping native interop test")
	}
	dir := t.TempDir()

	// A group keypair, materialized as the two native age files abc would write.
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	rcptFile := filepath.Join(dir, "recipients.txt")
	idFile := filepath.Join(dir, "identity.txt")
	if err := os.WriteFile(rcptFile, []byte(id.Recipient().String()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idFile, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("native age X25519 managed interop — no plugin\n")

	// --- Direction 1: abc-written (abccrypt) → stock `age -d -i identity.txt` ---
	abcFile := filepath.Join(dir, "viaabc.age")
	out, err := os.Create(abcFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := abccrypt.Encrypt(out, bytes.NewReader(plaintext), id.Recipient()); err != nil {
		t.Fatalf("abccrypt.Encrypt: %v", err)
	}
	_ = out.Close()

	dec := exec.Command(agePath, "-d", "-i", idFile, abcFile)
	got, err := dec.Output()
	if err != nil {
		t.Fatalf("age -d -i (abc→age): %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("abc→age mismatch:\n got %q\nwant %q", got, plaintext)
	}

	// --- Direction 2: stock `age -e -R recipients.txt` → abccrypt.Decrypt ---
	ageFile := filepath.Join(dir, "viaage.age")
	enc := exec.Command(agePath, "-e", "-R", rcptFile, "-o", ageFile)
	enc.Stdin = bytes.NewReader(plaintext)
	if b, err := enc.CombinedOutput(); err != nil {
		t.Fatalf("age -e -R (age→abc): %v\n%s", err, b)
	}
	f, err := os.Open(ageFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := abccrypt.Decrypt(f, id)
	if err != nil {
		t.Fatalf("abccrypt.Decrypt: %v", err)
	}
	got2, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read decrypted: %v", err)
	}
	if !bytes.Equal(got2, plaintext) {
		t.Fatalf("age→abc mismatch:\n got %q\nwant %q", got2, plaintext)
	}
}
