package data

// crypt_age.go — the age-envelope path (ADR-0067) for `abc data encrypt/decrypt`.
// New encryption is always age; decrypt auto-detects age vs the retired
// rclone-crypt format so existing `.encrypted` files still open. The crypto
// itself lives in internal/abccrypt (over filippo.io/age) — this file only wires
// it to the file paths + progress the commands already use.

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

// detectFileFormat opens path and returns its encryption format (age, legacy
// rclone-crypt, or unknown) for decrypt dispatch.
func detectFileFormat(path string) (abccrypt.Format, error) {
	f, err := os.Open(path)
	if err != nil {
		return abccrypt.FormatUnknown, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()
	format, _, err := abccrypt.DetectFormat(f)
	return format, err
}

// decryptDispatch decrypts srcPath into dstPath, choosing the engine from the
// file's on-disk format: age (ADR-0067) via ageIDs, or the retired rclone-crypt
// format via cryptor (back-compat for pre-age `.encrypted` files).
func decryptDispatch(ctx context.Context, srcPath, dstPath string, cryptor *cryptConfig, ageIDs []age.Identity) error {
	format, err := detectFileFormat(srcPath)
	if err != nil {
		return err
	}
	switch format {
	case abccrypt.FormatAge:
		if len(ageIDs) == 0 {
			return fmt.Errorf("%q is age-encrypted but no passphrase/identity is available to decrypt it", srcPath)
		}
		return ageDecryptToPath(ctx, srcPath, dstPath, ageIDs)
	case abccrypt.FormatRcloneLegacy:
		return cryptor.decryptToPath(srcPath, dstPath)
	default:
		return fmt.Errorf("unrecognised encryption format for %q (not an age or rclone-crypt file)", srcPath)
	}
}
