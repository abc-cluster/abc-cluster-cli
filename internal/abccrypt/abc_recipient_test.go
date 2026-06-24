package abccrypt

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
)

// mapKEKProvider is a test KEK source: kekID -> versioned 32-byte KEKs (1-indexed).
type mapKEKProvider struct{ keks map[string][][]byte }

func newKEKs(t *testing.T, ids ...string) *mapKEKProvider {
	m := &mapKEKProvider{keks: map[string][][]byte{}}
	for _, id := range ids {
		k := make([]byte, kekSize)
		if _, err := rand.Read(k); err != nil {
			t.Fatal(err)
		}
		m.keks[id] = [][]byte{k} // version 1
	}
	return m
}
func (m *mapKEKProvider) rotate(id string) {
	k := make([]byte, kekSize)
	_, _ = rand.Read(k)
	m.keks[id] = append(m.keks[id], k)
}
func (m *mapKEKProvider) WrapKEK(id string) ([]byte, int, error) {
	vs, ok := m.keks[id]
	if !ok || len(vs) == 0 {
		return nil, 0, fmt.Errorf("no KEK for %s", id)
	}
	return vs[len(vs)-1], len(vs), nil
}
func (m *mapKEKProvider) UnwrapKEK(id string, version int) ([]byte, error) {
	vs, ok := m.keks[id]
	if !ok || version < 1 || version > len(vs) {
		return nil, fmt.Errorf("no KEK %s v%d", id, version)
	}
	return vs[version-1], nil
}

func encryptAge(t *testing.T, plain []byte, r age.Recipient) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encrypt(&buf, bytes.NewReader(plain), r); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestABCRoundTrip(t *testing.T) {
	m := newKEKs(t, "group:demo")
	r, err := NewABCRecipient("group:demo", m)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("managed group file\n")
	ct := encryptAge(t, plain, r)

	got, err := Decrypt(bytes.NewReader(ct), NewABCIdentity(m))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(got)
	if !bytes.Equal(out, plain) {
		t.Fatalf("round-trip mismatch: %q", out)
	}
}

// G1 — splice resistance. Two files to the SAME group have different file keys;
// move file B's abc stanza body into file A's header. Decrypting A must fail,
// because A's header HMAC was computed with A's file key, not B's.
func TestABCStanzaSpliceFails(t *testing.T) {
	m := newKEKs(t, "group:demo")
	r, _ := NewABCRecipient("group:demo", m)
	a := encryptAge(t, bytes.Repeat([]byte("A"), 2000), r)
	b := encryptAge(t, bytes.Repeat([]byte("B"), 2000), r)

	spliced, ok := swapABCStanzaBody(a, b)
	if !ok {
		t.Skip("could not locate abc stanza body to splice (format change?)")
	}
	if _, err := Decrypt(bytes.NewReader(spliced), NewABCIdentity(m)); err == nil {
		t.Fatal("spliced file decrypted — G1 splice resistance FAILED")
	}
}

// swapABCStanzaBody replaces fileA's abc-stanza base64 body lines with fileB's.
// Both files share the same `-> abc ...` arg line (same recipient), so only the
// body (the wrapped key) differs. Returns (spliced, true) on success.
func swapABCStanzaBody(a, b []byte) ([]byte, bool) {
	bodyOf := func(f []byte) ([]string, int, int) { // lines, start, end (exclusive) of body
		lines := strings.Split(string(f), "\n")
		for i, ln := range lines {
			if strings.HasPrefix(ln, "-> "+abcStanzaType+" ") {
				start := i + 1
				end := start
				for end < len(lines) && !strings.HasPrefix(lines[end], "-> ") && !strings.HasPrefix(lines[end], "---") {
					end++
				}
				return lines, start, end
			}
		}
		return nil, 0, 0
	}
	al, as, ae := bodyOf(a)
	bl, bs, be := bodyOf(b)
	if al == nil || bl == nil || as == ae || bs == be {
		return nil, false
	}
	out := append([]string{}, al[:as]...)
	out = append(out, bl[bs:be]...)
	out = append(out, al[ae:]...)
	return []byte(strings.Join(out, "\n")), true
}

// G3 — flipping the version in the stanza args fails the AEAD tag (AAD mismatch).
func TestABCVersionTamperFails(t *testing.T) {
	m := newKEKs(t, "group:demo")
	m.rotate("group:demo") // now v2 is current
	r, _ := NewABCRecipient("group:demo", m)
	st, err := r.(*abcRecipient).Wrap(make([]byte, 16))
	if err != nil {
		t.Fatal(err)
	}
	if st[0].Args[2] != "2" {
		t.Fatalf("expected current version 2, got %s", st[0].Args[2])
	}
	st[0].Args[2] = "1" // downgrade
	if _, err := NewABCIdentity(m).Unwrap(st); err == nil {
		t.Fatal("version-tampered stanza unwrapped — G3 FAILED")
	}
}

// G3 — rewriting kek_id (caller is in both groups) fails the AEAD tag.
func TestABCKekIDTamperFails(t *testing.T) {
	m := newKEKs(t, "group:a", "group:b")
	r, _ := NewABCRecipient("group:a", m)
	st, _ := r.(*abcRecipient).Wrap(make([]byte, 16))
	st[0].Args[1] = "group:b"
	if _, err := NewABCIdentity(m).Unwrap(st); err == nil {
		t.Fatal("kek_id-tampered stanza unwrapped — G3 FAILED")
	}
}

// C2 — an unknown construction marker fails closed (no trial-decrypt fallback).
func TestABCConstructionTamperFails(t *testing.T) {
	m := newKEKs(t, "group:demo")
	r, _ := NewABCRecipient("group:demo", m)
	st, _ := r.(*abcRecipient).Wrap(make([]byte, 16))
	st[0].Args[0] = "kek-wrap-v2"
	if _, err := NewABCIdentity(m).Unwrap(st); err == nil {
		t.Fatal("unknown-construction stanza unwrapped — C2 FAILED")
	}
}

// G2 — many wraps under one (kek_id, version) produce distinct nonces.
func TestABCNonceUniqueness(t *testing.T) {
	m := newKEKs(t, "group:demo")
	r, _ := NewABCRecipient("group:demo", m)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		st, _ := r.(*abcRecipient).Wrap(make([]byte, 16))
		nonce := string(st[0].Body[:24])
		if seen[nonce] {
			t.Fatal("nonce reuse — G2 FAILED")
		}
		seen[nonce] = true
	}
}

func TestABCWrongKEKFailsClosed(t *testing.T) {
	enc := newKEKs(t, "group:demo")
	r, _ := NewABCRecipient("group:demo", enc)
	st, _ := r.(*abcRecipient).Wrap(make([]byte, 16))
	// A different identity whose KEK for group:demo is a different key.
	other := newKEKs(t, "group:demo")
	if _, err := NewABCIdentity(other).Unwrap(st); err == nil {
		t.Fatal("wrong KEK unwrapped — fail-closed FAILED")
	}
}

// C4 — kek_id grammar is enforced at the recipient boundary.
func TestABCKekIDGrammar(t *testing.T) {
	m := newKEKs(t, "group:demo")
	for _, bad := range []string{"group:de mo", "group:demo\n-> evil", "team:demo", "group:", "group:UPPER", ""} {
		if _, err := NewABCRecipient(bad, m); err == nil {
			t.Fatalf("accepted invalid kek_id %q — C4 FAILED", bad)
		}
	}
	if _, err := NewABCRecipient("user:solar-civet", m); err != nil {
		t.Fatalf("rejected valid kek_id: %v", err)
	}
}
