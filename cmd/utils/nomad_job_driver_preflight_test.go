package utils

import (
	"encoding/json"
	"testing"
)

func TestExtractJobTaskDrivers(t *testing.T) {
	raw := map[string]any{
		"TaskGroups": []any{
			map[string]any{
				"Tasks": []any{
					map[string]any{"Driver": "exec"},
					map[string]any{"Driver": "docker"},
				},
			},
			map[string]any{
				"Tasks": []any{
					map[string]any{"Driver": "exec"},
					map[string]any{"Driver": "containerd-driver"},
				},
			},
		},
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExtractJobTaskDrivers(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "docker", "containerd-driver"}
	if len(got) != len(want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
}

func TestExtractJobTaskDrivers_emptyGroups(t *testing.T) {
	raw := map[string]any{"TaskGroups": []any{}}
	b, _ := json.Marshal(raw)
	got, err := ExtractJobTaskDrivers(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %#v", got)
	}
}
