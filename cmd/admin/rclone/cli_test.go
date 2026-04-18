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

func TestParseABCRclonePreamble_DoubleDashInTailPassesThrough(t *testing.T) {
	_, _, _, rest, err := parseABCRclonePreamble("", []string{"--abc-local-config", "/cfg", "--", "--abc-server-config", "x"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--", "--abc-server-config", "x"}
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

func TestExtractRcloneCLIPassthrough_ExplicitDoubleDash(t *testing.T) {
	bin, srv, loc, rest, err := extractRcloneCLIPassthrough([]string{
		"--binary-location", "/bin/rclone", "--abc-server-config", "--", "lsd", "remote:",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/bin/rclone" || !srv || loc != "" {
		t.Fatalf("bin=%q srv=%v loc=%q", bin, srv, loc)
	}
	want := []string{"lsd", "remote:"}
	if len(rest) != len(want) {
		t.Fatalf("rest=%v", rest)
	}
	for i := range want {
		if rest[i] != want[i] {
			t.Fatalf("rest[%d]=%q want %q", i, rest[i], want[i])
		}
	}
}

func TestExtractRcloneCLIPassthrough_NonWrapperTailPassesThrough(t *testing.T) {
	bin, srv, loc, rest, err := extractRcloneCLIPassthrough([]string{"ls", "remote:", "--", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if srv || loc != "" {
		t.Fatalf("expected no abc flags, srv=%v loc=%q", srv, loc)
	}
	if bin != "" {
		t.Fatalf("unexpected bin %q", bin)
	}
	want := []string{"ls", "remote:", "--", "x"}
	if len(rest) != len(want) {
		t.Fatalf("rest=%v want %v", rest, want)
	}
	for i := range want {
		if rest[i] != want[i] {
			t.Fatalf("rest[%d]=%q want %q", i, rest[i], want[i])
		}
	}
}
