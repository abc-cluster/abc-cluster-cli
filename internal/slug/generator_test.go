package slug

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, ok := range []string{"abc", "abc-def-1", "cosmic-pelican-7", "xyz12"} {
		if err := Validate(ok); err != nil {
			t.Errorf("expected %q valid, got %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ab", "1abc", "Abc", "abc def", "abc--def-too-long-" + "x" + "yzabcdefghijklmnopqrstuvwxyz"} {
		if err := Validate(bad); err == nil {
			t.Errorf("expected %q invalid", bad)
		}
	}
}

func TestGenerateFormat(t *testing.T) {
	s := Generate()
	if err := Validate(s); err != nil {
		t.Errorf("Generate() produced invalid slug %q: %v", s, err)
	}
}

func TestGenerateUnique_NoCollision(t *testing.T) {
	s, err := GenerateUnique(func(string) (bool, error) { return false, nil }, 5)
	if err != nil {
		t.Fatal(err)
	}
	if s == "" {
		t.Fatal("empty slug")
	}
}

func TestGenerateUnique_Exhausted(t *testing.T) {
	_, err := GenerateUnique(func(string) (bool, error) { return true, nil }, 3)
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected ErrExhausted, got %v", err)
	}
}

func TestWordlistsSizes(t *testing.T) {
	if len(Adjectives) < 100 {
		t.Errorf("Adjectives too small: %d", len(Adjectives))
	}
	if len(Nouns) < 100 {
		t.Errorf("Nouns too small: %d", len(Nouns))
	}
}
