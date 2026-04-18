package serviceconfig

import "testing"

func TestFirstHostFromTraefikRule(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		got, ok := firstHostFromTraefikRule("Host(`grafana.aither`)")
		if !ok || got != "grafana.aither" {
			t.Fatalf("got %q ok=%v", got, ok)
		}
	})
	t.Run("compound", func(t *testing.T) {
		got, ok := firstHostFromTraefikRule("Host(`tusd.aither`) && PathPrefix(`/files/`)")
		if !ok || got != "tusd.aither" {
			t.Fatalf("got %q ok=%v", got, ok)
		}
	})
	t.Run("none", func(t *testing.T) {
		_, ok := firstHostFromTraefikRule("PathPrefix(`/only`)")
		if ok {
			t.Fatal("expected no Host")
		}
	})
}
