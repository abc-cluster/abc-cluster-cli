package rclone

import (
	"reflect"
	"testing"
)

func TestParseABCRclonePreamble(t *testing.T) {
	bin, srv, loc, rest, err := parseABCRclonePreamble("/x/bin/rclone", []string{
		"--abc-server-config", "ls", "remote:",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/x/bin/rclone" || !srv || loc != "" {
		t.Fatalf("unexpected preamble: bin=%q srv=%v loc=%q", bin, srv, loc)
	}
	want := []string{"ls", "remote:"}
	if !reflect.DeepEqual(rest, want) {
		t.Fatalf("rest=%v want %v", rest, want)
	}
}

func TestParseABCRclonePreamble_DoubleDash(t *testing.T) {
	_, _, _, rest, err := parseABCRclonePreamble("", []string{"--abc-local-config", "/cfg", "--", "--abc-server-config", "x"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--abc-server-config", "x"}
	if !reflect.DeepEqual(rest, want) {
		t.Fatalf("rest=%v want %v", rest, want)
	}
}

func TestParseABCRclonePreamble_Conflict(t *testing.T) {
	_, _, _, _, err := parseABCRclonePreamble("", []string{"--abc-server-config", "--abc-local-config=/a"})
	if err == nil {
		t.Fatal("expected error")
	}
}
