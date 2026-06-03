// Package supportbundle assembles a single, human-readable, credential-safe
// support bundle from already-redacted sections, applying a defence-in-depth
// redaction guarantee before the text is written to disk.
//
// The bundle is the artifact a closed-beta user hands the abc team when they hit
// an issue we can't reproduce: it leads with the CLI version (the #1 suspected
// cause), carries the resolved-but-redacted config, the live doctor checks, and
// the failing command's debug trace — without leaking the user's credentials.
//
// Three redaction layers (see brainstorms/cli-support-bundle/):
//
//	Layer 1 — source-level: every Section.Body is rendered from an
//	          already-redacting source (config.maskToken, the RedactingHandler's
//	          debug log, masked env). Callers own this.
//	Layer 2 — known-secret exact-value scrub (ScrubKnownSecrets): the bundler
//	          knows the user's actual secret values and find-replaces each one
//	          across the whole assembled text. This is the strongest guarantee
//	          for the user's OWN credentials and — crucially — preserves
//	          UUID-shaped non-secrets (alloc/eval/job IDs) because they don't
//	          equal a known secret.
//	Layer 3 — value-pattern catch-all (debuglog.RedactText): a final net for
//	          UNKNOWN secrets whose value we didn't have (Bearer …, tskey-auth-…,
//	          PEM blocks, scheme://user:pass@…).
package supportbundle

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
)

// Format is the bundle format version, stamped in the header so the abc team can
// tell which assembler produced a given file.
const Format = 1

// minScrubLen is the shortest known-secret value the Layer-2 scrub will act on.
// Anything shorter is skipped to avoid nuking short non-secret substrings (e.g.
// a 4-char salt fragment) wholesale. Real credentials in this deployment are
// UUIDs / long tokens, well above this floor.
const minScrubLen = 6

// RedactedSecret is the placeholder Layer 2 leaves where a known secret was.
const RedactedSecret = "[REDACTED:secret]"

// Section is one titled block of the bundle. Body must already be source-level
// redacted (Layer 1) by the caller; Assemble adds Layers 2 and 3 over the whole.
type Section struct {
	Title string
	Body  string
}

// Input is everything Assemble needs. GeneratedAt is passed in (not read from a
// clock here) so the assembler stays deterministic and unit-testable.
type Input struct {
	GeneratedAt string    // RFC3339 timestamp, stamped by the caller
	Whoami      string    // active-context identity, for the header + filename
	Sections    []Section // pre-rendered, source-redacted
	Secrets     []string  // raw known-secret values for the Layer-2 exact scrub
}

// Assemble renders the full bundle text and applies Layers 2 and 3. The result
// is safe to write to a shared file: the user can open it and see [REDACTED…]
// wherever a secret was.
func Assemble(in Input) string {
	var b strings.Builder

	b.WriteString("# abc support bundle\n")
	fmt.Fprintf(&b, "generated: %s   format: %d\n", in.GeneratedAt, Format)
	if strings.TrimSpace(in.Whoami) != "" {
		fmt.Fprintf(&b, "user: %s\n", in.Whoami)
	}
	b.WriteString("This file is redacted: credentials, tokens and secret keys are removed.\n")
	b.WriteString("Open it and read it before sharing — you should see " + RedactedSecret + " (and other [REDACTED…] markers) where secrets were.\n\n")

	for _, s := range in.Sections {
		fmt.Fprintf(&b, "## %s\n", s.Title)
		body := strings.TrimRight(s.Body, "\n")
		if body != "" {
			b.WriteString(body)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	text := b.String()
	text = ScrubKnownSecrets(text, in.Secrets) // Layer 2
	text = debuglog.RedactText(text)           // Layer 3
	return text
}

// ScrubKnownSecrets replaces every occurrence of each known secret value in text
// with RedactedSecret. Secrets are scrubbed longest-first so that a secret which
// contains a shorter secret as a substring is removed completely before the
// shorter one runs. Values shorter than minScrubLen are ignored.
func ScrubKnownSecrets(text string, secrets []string) string {
	for _, s := range dedupeLongestFirst(secrets) {
		if len(s) < minScrubLen {
			continue
		}
		text = strings.ReplaceAll(text, s, RedactedSecret)
	}
	return text
}

func dedupeLongestFirst(secrets []string) []string {
	seen := make(map[string]bool, len(secrets))
	out := make([]string, 0, len(secrets))
	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}
