package abccrypt

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestPassphraseRoundTrip(t *testing.T) {
	const pw = "correct horse battery staple"
	plain := []byte("sensitive genomic sample manifest\n")

	rcpt, err := PassphraseRecipient(pw)
	if err != nil {
		t.Fatal(err)
	}
	var ct bytes.Buffer
	if err := Encrypt(&ct, bytes.NewReader(plain), rcpt); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(ct.Bytes(), ageHeaderMagic) {
		t.Fatalf("ciphertext is not an age file: %q", ct.Bytes()[:20])
	}

	id, err := PassphraseIdentity(pw)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Decrypt(bytes.NewReader(ct.Bytes()), id)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch: got %q", got)
	}
}

// TestStockAgeInterop confirms a passphrase file is decryptable by the stock age
// library exactly as the `age` CLI would (no lock-in — ADR-0067).
func TestStockAgeInterop(t *testing.T) {
	const pw = "interop-pass"
	plain := []byte("hello stock age")

	rcpt, _ := PassphraseRecipient(pw)
	var ct bytes.Buffer
	if err := Encrypt(&ct, bytes.NewReader(plain), rcpt); err != nil {
		t.Fatal(err)
	}
	// Decrypt with a vanilla age.ScryptIdentity (what `age -d` uses).
	id, err := age.NewScryptIdentity(pw)
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(ct.Bytes()), id)
	if err != nil {
		t.Fatalf("stock age failed to decrypt abc file: %v", err)
	}
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, plain) {
		t.Fatalf("stock-age mismatch: %q", got)
	}
}

func TestWrongPassphraseFailsClosed(t *testing.T) {
	rcpt, _ := PassphraseRecipient("right")
	var ct bytes.Buffer
	_ = Encrypt(&ct, strings.NewReader("secret"), rcpt)

	id, _ := PassphraseIdentity("wrong")
	if _, err := Decrypt(bytes.NewReader(ct.Bytes()), id); err == nil {
		t.Fatal("decrypt with wrong passphrase succeeded — must fail closed")
	}
}

func TestTamperedBodyFailsClosed(t *testing.T) {
	rcpt, _ := PassphraseRecipient("pw")
	var ct bytes.Buffer
	_ = Encrypt(&ct, bytes.NewReader(bytes.Repeat([]byte("A"), 4096)), rcpt)
	b := ct.Bytes()
	b[len(b)-10] ^= 0xff // flip a byte in the STREAM body

	id, _ := PassphraseIdentity("pw")
	r, err := Decrypt(bytes.NewReader(b), id)
	if err == nil {
		// header parsed; the AEAD failure must surface on read, no plaintext.
		if _, rerr := io.ReadAll(r); rerr == nil {
			t.Fatal("tampered body decrypted without error — must fail closed")
		}
	}
}

func TestX25519RoundTrip(t *testing.T) {
	idObj, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	rcpt, err := X25519Recipient(idObj.Recipient().String())
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("public-key path")
	var ct bytes.Buffer
	if err := Encrypt(&ct, bytes.NewReader(plain), rcpt); err != nil {
		t.Fatal(err)
	}
	id, err := X25519Identity(idObj.String())
	if err != nil {
		t.Fatal(err)
	}
	r, _ := Decrypt(bytes.NewReader(ct.Bytes()), id)
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, plain) {
		t.Fatalf("x25519 round-trip mismatch: %q", got)
	}
}

func TestDetectFormat(t *testing.T) {
	// age
	rcpt, _ := PassphraseRecipient("pw")
	var ageCT bytes.Buffer
	_ = Encrypt(&ageCT, strings.NewReader("x"), rcpt)
	if f, _, _ := DetectFormat(bytes.NewReader(ageCT.Bytes())); f != FormatAge {
		t.Fatalf("expected FormatAge, got %v", f)
	}
	// rclone legacy magic
	legacy := append([]byte("RCLONE\x00\x00"), bytes.Repeat([]byte{0}, 40)...)
	if f, _, _ := DetectFormat(bytes.NewReader(legacy)); f != FormatRcloneLegacy {
		t.Fatalf("expected FormatRcloneLegacy, got %v", f)
	}
	// unknown — and the replay reader must preserve the bytes
	raw := []byte("just some plaintext bytes here")
	f, replay, _ := DetectFormat(bytes.NewReader(raw))
	if f != FormatUnknown {
		t.Fatalf("expected FormatUnknown, got %v", f)
	}
	got, _ := io.ReadAll(replay)
	if !bytes.Equal(got, raw) {
		t.Fatalf("replay reader lost bytes: got %q want %q", got, raw)
	}
}
