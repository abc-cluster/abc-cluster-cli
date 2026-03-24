package data

import (
	"bytes"
	"fmt"
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
