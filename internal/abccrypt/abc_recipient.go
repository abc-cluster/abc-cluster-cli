package abccrypt

// abc_recipient.go — the managed "abc recipient" (ADR-0067 Phase 2): a real
// age.Recipient/Identity that wraps age's per-file key under a broker-managed
// KEK (group:<g> / user:<slot>), self-describing via a kek_id+version stanza.
//
// This is the ONLY bespoke-crypto surface in ADR-0067. It cleared the G5
// security review (PASS-WITH-CONDITIONS, 2026-06-12); the conditions C1–C6 are
// implemented here:
//   G1  real age.Recipient/Identity → age's header HMAC covers the stanza →
//       stanza-splice fails closed (proved by the age-level splice test).
//   G2  wrap key = HKDF-SHA256(KEK, info = construction|kek_id|version);
//       XChaCha20-Poly1305 with a 24-byte random nonce (no nonce-reuse break);
//       the raw KEK is NEVER the AEAD key.
//   G3/C2/C3  construction marker + kek_id + version are in the AEAD additional
//       data (tamper/downgrade fails the tag); construction marker is explicit
//       and in-band — NO trial-decrypt across constructions.
//   C4  kek_id grammar enforced at the type boundary; C3 version canonical.
//   C5  the wrapped plaintext is age's 16-byte file key; the KEK is 32 bytes.
//
// Key delivery (seedling, per the 2026-06-12 tiering decision) is KEK-release:
// the KEKProvider fetches K_G once and the wrap/unwrap happen LOCALLY here. The
// grove per-file broker-unwrap variant implements the same KEKProvider contract
// against /keys/unwrap instead.

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"filippo.io/age"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	abcStanzaType   = "abc"
	abcConstruction = "kek-wrap-v1" // bump only with a new, AAD-bound construction
	kekSize         = 32            // KEK is 32 bytes (C5); the wrapped file key is 16
)

var (
	kekIDRe   = regexp.MustCompile(`^(group|user):[a-z0-9_-]{1,64}$`) // C4
	versionRe = regexp.MustCompile(`^[1-9][0-9]*$`)                   // C3 canonical
)

// KEKProvider supplies broker-managed KEK material. Seedling: fetch K_G via
// /keys/get once and cache; wrap/unwrap happen locally. Grove regulated: a
// variant that calls /keys/unwrap per file (Jurist-gated) satisfies the same
// interface. The raw KEK never appears in a file — only the wrapped file key.
type KEKProvider interface {
	// WrapKEK returns the current KEK material + version for kekID (encrypt side).
	WrapKEK(kekID string) (kek []byte, version int, err error)
	// UnwrapKEK returns the KEK material for a specific (kekID, version) (decrypt).
	// It MUST use the version it is given — never substitute "latest" (G3).
	UnwrapKEK(kekID string, version int) (kek []byte, err error)
}

// domainContext is the byte string bound into BOTH the HKDF info and the AEAD
// additional data, so construction-version / kek_id / version are authenticated.
func domainContext(kekID, version string) []byte {
	return []byte(abcConstruction + "\x00" + kekID + "\x00" + version)
}

func deriveWrapKey(kek []byte, kekID, version string) ([]byte, error) {
	r := hkdf.New(sha256.New, kek, nil, domainContext(kekID, version))
	wk := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(r, wk); err != nil {
		return nil, fmt.Errorf("hkdf: %w", err)
	}
	return wk, nil
}

// ── Recipient (encrypt side) ──────────────────────────────────────────────

type abcRecipient struct {
	kekID string
	kek   KEKProvider
}

// NewABCRecipient builds the managed recipient for kekID (group:<g>|user:<slot>).
func NewABCRecipient(kekID string, kek KEKProvider) (age.Recipient, error) {
	if !kekIDRe.MatchString(kekID) {
		return nil, fmt.Errorf("invalid kek_id %q (want group:<g>|user:<slot> over [a-z0-9_-])", kekID)
	}
	if kek == nil {
		return nil, fmt.Errorf("nil KEK provider")
	}
	return &abcRecipient{kekID: kekID, kek: kek}, nil
}

func (r *abcRecipient) Wrap(fileKey []byte) ([]*age.Stanza, error) {
	kek, ver, err := r.kek.WrapKEK(r.kekID)
	if err != nil {
		return nil, fmt.Errorf("get KEK for %s: %w", r.kekID, err)
	}
	if len(kek) != kekSize {
		return nil, fmt.Errorf("KEK must be %d bytes, got %d", kekSize, len(kek))
	}
	version := strconv.Itoa(ver)
	if !versionRe.MatchString(version) {
		return nil, fmt.Errorf("non-canonical KEK version %q", version)
	}
	wk, err := deriveWrapKey(kek, r.kekID, version)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(wk)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize()) // 24 bytes
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	ct := aead.Seal(nil, nonce, fileKey, domainContext(r.kekID, version))
	body := make([]byte, 0, len(nonce)+len(ct))
	body = append(body, nonce...)
	body = append(body, ct...)
	return []*age.Stanza{{
		Type: abcStanzaType,
		Args: []string{abcConstruction, r.kekID, version},
		Body: body,
	}}, nil
}

// ── Identity (decrypt side) ───────────────────────────────────────────────

type abcIdentity struct {
	kek KEKProvider
}

// NewABCIdentity builds the managed identity. Unwrap is local (uses the KEK the
// provider returns); a tampered/wrong stanza fails the AEAD tag, never garbage.
func NewABCIdentity(kek KEKProvider) age.Identity {
	return &abcIdentity{kek: kek}
}

func (id *abcIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	for _, s := range stanzas {
		if s.Type != abcStanzaType {
			continue
		}
		if len(s.Args) != 3 {
			return nil, fmt.Errorf("abc stanza: expected 3 args, got %d", len(s.Args))
		}
		construction, kekID, version := s.Args[0], s.Args[1], s.Args[2]
		if construction != abcConstruction {
			// C2: explicit construction selection, no trial-decrypt across versions.
			return nil, fmt.Errorf("abc stanza: unknown construction %q (no fallback)", construction)
		}
		if !kekIDRe.MatchString(kekID) { // C4
			return nil, fmt.Errorf("abc stanza: invalid kek_id %q", kekID)
		}
		if !versionRe.MatchString(version) { // C3
			return nil, fmt.Errorf("abc stanza: non-canonical version %q", version)
		}
		v, _ := strconv.Atoi(version)
		kek, err := id.kek.UnwrapKEK(kekID, v)
		if err != nil {
			return nil, fmt.Errorf("get KEK %s v%d: %w", kekID, v, err)
		}
		if len(kek) != kekSize {
			return nil, fmt.Errorf("KEK must be %d bytes, got %d", kekSize, len(kek))
		}
		wk, err := deriveWrapKey(kek, kekID, version)
		if err != nil {
			return nil, err
		}
		aead, err := chacha20poly1305.NewX(wk)
		if err != nil {
			return nil, err
		}
		if len(s.Body) < aead.NonceSize() {
			return nil, fmt.Errorf("abc stanza: body too short")
		}
		nonce, ct := s.Body[:aead.NonceSize()], s.Body[aead.NonceSize():]
		fileKey, err := aead.Open(nil, nonce, ct, domainContext(kekID, version))
		if err != nil {
			// Wrong/rotated KEK, or tampered kek_id/version/construction → fail closed.
			return nil, fmt.Errorf("abc unwrap failed (wrong/rotated KEK or tampered stanza)")
		}
		return fileKey, nil
	}
	return nil, age.ErrIncorrectIdentity
}
