package data

import "testing"

func TestPlacementConstraintPreamble(t *testing.T) {
	t.Parallel()
	if got := placementConstraintPreamble(""); got != "" {
		t.Fatalf("empty node: got %q", got)
	}
	id := "a1b2c3d4-e5f6-4789-a012-345678901234"
	got := placementConstraintPreamble(id)
	want := "#ABC --constraint=node.unique.id==" + id
	if got != want {
		t.Fatalf("uuid: got %q want %q", got, want)
	}
	got2 := placementConstraintPreamble("compute-node-01")
	want2 := "#ABC --constraint=node.unique.name==compute-node-01"
	if got2 != want2 {
		t.Fatalf("name: got %q want %q", got2, want2)
	}
}
