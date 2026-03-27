package data

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/secretbox"
)

func TestEncryptedSize(t *testing.T) {
	tests := []struct {
		plain    int64
		expected int64
	}{
		{0, int64(rcloneFileHeaderSize)},
		{1, int64(rcloneFileHeaderSize + 1 + rcloneBlockHeaderSize)},
		{rcloneBlockDataSize, int64(rcloneFileHeaderSize + rcloneBlockDataSize + rcloneBlockHeaderSize)},
		{rcloneBlockDataSize + 1, int64(rcloneFileHeaderSize + rcloneBlockDataSize + 1 + 2*rcloneBlockHeaderSize)},
	}
	for _, test := range tests {
		if got := encryptedSize(test.plain); got != test.expected {
			t.Fatalf("encryptedSize(%d) = %d, want %d", test.plain, got, test.expected)
		}
	}
}

func TestEncryptStreamRoundTrip(t *testing.T) {
	plaintext := bytes.Repeat([]byte("abc123"), 5000)
	randSource := bytes.NewReader(make([]byte, rcloneFileNonceSize))
	cryptor, err := newCryptConfig("password", "salt", randSource)
	if err != nil {
		t.Fatalf("newCryptConfig: %v", err)
	}

	var encrypted bytes.Buffer
	written, err := cryptor.encryptStream(&encrypted, bytes.NewReader(plaintext))
	if err != nil {
		t.Fatalf("encryptStream: %v", err)
	}
	if expected := encryptedSize(int64(len(plaintext))); written != expected {
		t.Fatalf("written size = %d, want %d", written, expected)
	}

	decrypted, err := decryptRclone(encrypted.Bytes(), cryptor.dataKey)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted data mismatch")
	}
}

func decryptRclone(ciphertext []byte, key [32]byte) ([]byte, error) {
	if len(ciphertext) < rcloneFileHeaderSize {
		return nil, errInvalidEncryptedSize(len(ciphertext))
	}
	if string(ciphertext[:len(rcloneFileMagic)]) != rcloneFileMagic {
		return nil, errInvalidEncryptedMagic()
	}
	var n nonce
	copy(n[:], ciphertext[len(rcloneFileMagic):rcloneFileHeaderSize])

	out := &bytes.Buffer{}
	offset := rcloneFileHeaderSize
	for offset < len(ciphertext) {
		remaining := len(ciphertext) - offset
		blockLen := rcloneBlockHeaderSize + rcloneBlockDataSize
		if remaining < blockLen {
			blockLen = remaining
		}
		block := ciphertext[offset : offset+blockLen]
		plain, ok := secretbox.Open(nil, block, n.pointer(), &key)
		if !ok {
			return nil, errDecryptFailed()
		}
		out.Write(plain)
		n.increment()
		offset += blockLen
	}
	return out.Bytes(), nil
}

func errInvalidEncryptedSize(size int) error {
	return &cryptTestError{msg: "encrypted data too short", size: size}
}

func errInvalidEncryptedMagic() error {
	return &cryptTestError{msg: "invalid magic header"}
}

func errDecryptFailed() error {
	return &cryptTestError{msg: "decrypt failed"}
}

func TestUploadTempDir_DefaultsToHomeDotAbc(t *testing.T) {
	t.Setenv("ABC_DATA_UPLOAD_TMPDIR", "") // ensure env var is not set
	dir, err := uploadTempDir()
	if err != nil {
		t.Fatalf("uploadTempDir: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".abc", "tmpdir")
	if dir != want {
		t.Fatalf("uploadTempDir() = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("expected directory to be created at %q", dir)
	}
}

func TestUploadTempDir_RespectsEnvVar(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom-tmpdir")
	t.Setenv("ABC_DATA_UPLOAD_TMPDIR", custom)
	dir, err := uploadTempDir()
	if err != nil {
		t.Fatalf("uploadTempDir: %v", err)
	}
	if dir != custom {
		t.Fatalf("uploadTempDir() = %q, want %q", dir, custom)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("expected directory to be created at %q", dir)
	}
}

func TestEncryptTempFile_LandsInUploadTempDir(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "enc-tmpdir")
	t.Setenv("ABC_DATA_UPLOAD_TMPDIR", custom)

	src := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(src, []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}
	randSource := bytes.NewReader(make([]byte, rcloneFileNonceSize))
	cryptor, err := newCryptConfig("pass", "salt", randSource)
	if err != nil {
		t.Fatal(err)
	}

	tmpPath, cleanup, err := cryptor.encryptToTempFileWithProgress(context.Background(), src, nil)
	if err != nil {
		t.Fatalf("encryptToTempFileWithProgress: %v", err)
	}
	defer cleanup() //nolint:errcheck

	if !strings.HasPrefix(tmpPath, custom) {
		t.Fatalf("temp file %q not inside expected dir %q", tmpPath, custom)
	}
}

func TestEncryptStream_ContextCancellationStopsEncryption(t *testing.T) {
	// Build a large-enough plaintext that encryption takes multiple blocks.
	plaintext := bytes.Repeat([]byte("x"), rcloneBlockDataSize*10)
	randSource := bytes.NewReader(make([]byte, rcloneFileNonceSize))
	cryptor, err := newCryptConfig("pass", "salt", randSource)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so the first ctx.Err() check fires

	var dst bytes.Buffer
	_, err = cryptor.encryptStreamWithProgress(ctx, &dst, bytes.NewReader(plaintext), nil)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context canceled error, got: %v", err)
	}
}

type cryptTestError struct {
	msg  string
	size int
}

func (e *cryptTestError) Error() string {
	if e.size == 0 {
		return e.msg
	}
	return fmt.Sprintf("%s (size=%d)", e.msg, e.size)
}
