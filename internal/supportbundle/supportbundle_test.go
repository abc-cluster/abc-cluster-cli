package supportbundle

import (
	"strings"
	"testing"
)

// sentinelSecret is a UUID-shaped fixture standing in for the real unified
// credential (access_key == secret_key == nomad token). The whole point of the
// bundle is that THIS string never reaches the output.
const sentinelSecret = "6952c4a1-7b3e-4f0a-9c2d-deadbeef0042"

// TestAssemble_SecretNeverAppears is the redaction guarantee: a section that
// accidentally echoes the secret (a token query param, an error message quoting
// it, a raw config dump) must not survive assembly. The exact-value scrub
// (Layer 2) removes it everywhere.
func TestAssemble_SecretNeverAppears(t *testing.T) {
	in := Input{
		GeneratedAt: "2026-06-03T12:00:00Z",
		Whoami:      "devon",
		Secrets:     []string{sentinelSecret},
		Sections: []Section{
			{Title: "1. version", Body: "abc v0.1.37"},
			// Three places a careless section could leak the secret:
			{Title: "3. config", Body: "access_token " + sentinelSecret},
			{Title: "7. debug trace", Body: `{"msg":"http.request","url":"https://up/?token=` + sentinelSecret + `"}`},
			{Title: "x. error", Body: "upload failed: bad token " + sentinelSecret + " rejected"},
		},
	}

	out := Assemble(in)

	if n := strings.Count(out, sentinelSecret); n != 0 {
		t.Fatalf("secret leaked: appeared %d time(s) in bundle:\n%s", n, out)
	}
	if !strings.Contains(out, RedactedSecret) {
		t.Fatalf("expected %q markers where the secret was, found none:\n%s", RedactedSecret, out)
	}
}

// TestScrub_PreservesNonSecretUUIDs proves the needle is threaded: UUID-shaped
// IDs that are NOT the known secret (alloc/eval/job IDs — exactly the debugging
// context we want to keep) must survive.
func TestScrub_PreservesNonSecretUUIDs(t *testing.T) {
	allocID := "a1b2c3d4-0000-1111-2222-333344445555" // not the secret
	text := "alloc=" + allocID + " token=" + sentinelSecret
	out := ScrubKnownSecrets(text, []string{sentinelSecret})

	if !strings.Contains(out, allocID) {
		t.Fatalf("alloc ID was wrongly scrubbed; want it preserved:\n%s", out)
	}
	if strings.Contains(out, sentinelSecret) {
		t.Fatalf("secret survived the scrub:\n%s", out)
	}
}

// TestScrub_LongestFirst ensures a secret containing a shorter secret as a
// substring is fully removed.
func TestScrub_LongestFirst(t *testing.T) {
	short := "abcdef0123"
	long := short + "-extended-tail-9999"
	text := "a=" + long + " b=" + short
	out := ScrubKnownSecrets(text, []string{short, long})

	if strings.Contains(out, short) {
		t.Fatalf("a secret substring survived:\n%s", out)
	}
}

// TestScrub_IgnoresTooShort guards against nuking short non-secret substrings.
func TestScrub_IgnoresTooShort(t *testing.T) {
	text := "namespace su-mbhg-hostgen value=ab"
	out := ScrubKnownSecrets(text, []string{"ab"}) // below minScrubLen
	if out != text {
		t.Fatalf("a too-short secret was scrubbed; want no-op:\n got: %q\nwant: %q", out, text)
	}
}

// TestAssemble_Layer3CatchAll proves the value-pattern net catches an UNKNOWN
// secret (one whose value we didn't have in Secrets) — here a Bearer token.
func TestAssemble_Layer3CatchAll(t *testing.T) {
	in := Input{
		GeneratedAt: "2026-06-03T12:00:00Z",
		Sections: []Section{
			{Title: "7. debug trace", Body: "Authorization: Bearer abcDEF1234567890ghiJKL9876"},
		},
	}
	out := Assemble(in)
	if strings.Contains(out, "abcDEF1234567890ghiJKL9876") {
		t.Fatalf("Layer 3 failed to catch an unknown Bearer token:\n%s", out)
	}
}
