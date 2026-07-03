package appgen

import "testing"

func TestGenerateSecretKey(t *testing.T) {
	a, err := generateSecretKey()
	if err != nil {
		t.Fatalf("generateSecretKey: %v", err)
	}
	if len(a) != 40 {
		t.Fatalf("expected a 40-character secret, got %d chars: %q", len(a), a)
	}
	b, err := generateSecretKey()
	if err != nil {
		t.Fatalf("generateSecretKey (second call): %v", err)
	}
	if a == b {
		t.Fatal("two calls produced the same secret — not random")
	}
}
