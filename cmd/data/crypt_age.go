package data

// crypt_age.go — the age-envelope path (ADR-0067) for `abc data encrypt/decrypt`.
// Encryption is always age; decrypt is age-only (rclone-crypt back-compat was
// dropped 2026-06-12 — no live users). DetectFormat replays the consumed header
// bytes so age.Decrypt works on pipes/stdin; a legacy rclone-crypt file is no
// longer decryptable. The crypto itself lives in internal/abccrypt (over
// filippo.io/age) — this file only wires it to the file paths + progress the
// commands already use.

import (
	"context"
	"fmt"
	"io"
	"os"

	"filippo.io/age"

	"github.com/abc-cluster/abc-cluster-cli/internal/abccrypt"
)

// ageEncryptToPath encrypts srcPath into dstPath as an age file wrapped to rcpts.
func ageEncryptToPath(_ context.Context, srcPath, dstPath string, rcpts []age.Recipient, onProgress func(int64)) (err error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer func() {
		if cerr := dst.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	var r io.Reader = src
	if onProgress != nil {
		r = &progressReader{reader: src, onRead: onProgress}
	}
	return abccrypt.Encrypt(dst, r, rcpts...)
}

// ageDecryptToPath decrypts an age file at srcPath into dstPath using ids.
func ageDecryptToPath(_ context.Context, srcPath, dstPath string, ids []age.Identity) (err error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	_, replay, derr := abccrypt.DetectFormat(src)
	if derr != nil {
		return derr
	}
	r, err := abccrypt.Decrypt(replay, ids...)
	if err != nil {
		return err
	}

	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer func() {
		if cerr := dst.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if _, err = io.Copy(dst, r); err != nil {
		return fmt.Errorf("decrypt body (verify failed or corrupt): %w", err)
	}
	return nil
}

// (Legacy rclone-crypt decrypt dispatch removed 2026-06-12 — no live users; decrypt
// is age-only. age.Decrypt matches the right stanza across the supplied identities
// — passphrase, managed abc recipient, or X25519 — so no format branching is needed.)
