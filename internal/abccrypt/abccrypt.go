// Package abccrypt is the shared encryption library for ABC-cluster (ADR-0067):
// an age-based envelope — each file gets a random per-file data key, the body is
// encrypted with age's ChaCha20-Poly1305 STREAM, and the data key is wrapped to
// one or more recipient stanzas in age's self-describing header.
//
// Phase 1 (this file) covers the UNGATED, stock-age recipients only:
//   - passphrase  (age scrypt; decryptable by stock `age`/`rage`)
//   - X25519      (age public-key; cross-recipient sharing)
//
// The managed "abc recipient" (a broker-managed KEK wrapping the DEK, encoding
// kek_id+version) is the bespoke surface gated by the spec's G1–G5 security
// review and is NOT implemented here — it plugs in later as an age.Recipient /
// age.Identity behind the same Encrypt/Decrypt entry points.
//
// Never reimplement age or its primitives — this package only composes
// filippo.io/age (ADR-0067 / specs/active/abc-managed-encryption-keys.md).
package abccrypt

import (
	"bytes"
	"fmt"
	"io"

	"filippo.io/age"
)

// Suffix is the extension for new age-envelope artifacts produced by abc.
const Suffix = ".age"

// LegacySuffix is the extension produced by the retired rclone-crypt path
// (ADR-0066). New files are never written with it, but DetectFormat + the
// decrypt path still read it for backward compatibility with existing data.
const LegacySuffix = ".encrypted"

// ageHeaderMagic is the first bytes of every age v1 file header.
var ageHeaderMagic = []byte("age-encryption.org/v1")

// rcloneFileMagic is the first bytes of a legacy rclone-crypt file.
var rcloneFileMagic = []byte("RCLONE\x00\x00")

// Format identifies the on-disk encryption format of an artifact.
type Format int

const (
	FormatUnknown Format = iota
	FormatAge            // age envelope (ADR-0067) — this package
	FormatRcloneLegacy   // legacy rclone-crypt (ADR-0066) — decrypt-only back-compat
)

func (f Format) String() string {
	switch f {
	case FormatAge:
		return "age"
	case FormatRcloneLegacy:
		return "rclone-crypt (legacy)"
	default:
		return "unknown"
	}
}

// detectLen is enough bytes to recognise either magic.
const detectLen = 21 // len("age-encryption.org/v1")

// DetectFormat classifies an artifact from its leading bytes. It reads at most
// detectLen bytes from r and returns the format plus a reader that replays the
// consumed bytes followed by the remainder (so the caller can decrypt without
// re-seeking — works on pipes/stdin).
func DetectFormat(r io.Reader) (Format, io.Reader, error) {
	head := make([]byte, detectLen)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return FormatUnknown, nil, fmt.Errorf("read header: %w", err)
	}
	head = head[:n]
	replay := io.MultiReader(bytes.NewReader(head), r)

	switch {
	case bytes.HasPrefix(head, ageHeaderMagic):
		return FormatAge, replay, nil
	case bytes.HasPrefix(head, rcloneFileMagic):
		return FormatRcloneLegacy, replay, nil
	default:
		return FormatUnknown, replay, nil
	}
}

// PassphraseRecipient returns an age scrypt recipient for an operator-blind /
// BYO-passphrase file. The resulting artifact is decryptable by stock `age`.
// The passphrase must be non-empty.
func PassphraseRecipient(passphrase string) (age.Recipient, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("passphrase must not be empty")
	}
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("scrypt recipient: %w", err)
	}
	return r, nil
}

// PassphraseIdentity returns an age scrypt identity for decrypting a
// passphrase-encrypted file.
func PassphraseIdentity(passphrase string) (age.Identity, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("passphrase must not be empty")
	}
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("scrypt identity: %w", err)
	}
	return id, nil
}

// X25519Recipient parses an age public key ("age1…") into a recipient.
func X25519Recipient(pubkey string) (age.Recipient, error) {
	r, err := age.ParseX25519Recipient(pubkey)
	if err != nil {
		return nil, fmt.Errorf("parse age recipient: %w", err)
	}
	return r, nil
}

// X25519Identity parses an age secret key ("AGE-SECRET-KEY-1…") into an identity.
func X25519Identity(secretKey string) (age.Identity, error) {
	id, err := age.ParseX25519Identity(secretKey)
	if err != nil {
		return nil, fmt.Errorf("parse age identity: %w", err)
	}
	return id, nil
}

// Encrypt writes an age-encrypted copy of src to dst, wrapped to every recipient.
// At least one recipient is required. The data key is per-file and random; the
// body uses age's ChaCha20-Poly1305 STREAM. Encrypt closes the age writer (it
// does NOT close dst — the caller owns dst).
func Encrypt(dst io.Writer, src io.Reader, recipients ...age.Recipient) error {
	if len(recipients) == 0 {
		return fmt.Errorf("at least one recipient is required")
	}
	w, err := age.Encrypt(dst, recipients...)
	if err != nil {
		return fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := io.Copy(w, src); err != nil {
		_ = w.Close()
		return fmt.Errorf("encrypt body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finalize age stream: %w", err)
	}
	return nil
}

// Decrypt returns a reader over the plaintext of an age artifact, trying each
// identity. Verify-before-use is intrinsic: a wrong key fails the header HMAC /
// AEAD and surfaces as an error here, never as garbage plaintext. At least one
// identity is required.
func Decrypt(src io.Reader, identities ...age.Identity) (io.Reader, error) {
	if len(identities) == 0 {
		return nil, fmt.Errorf("at least one identity is required")
	}
	r, err := age.Decrypt(src, identities...)
	if err != nil {
		return nil, fmt.Errorf("age decrypt: %w", err)
	}
	return r, nil
}
