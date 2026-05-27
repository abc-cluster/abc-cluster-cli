package state

import (
	cryptorand "crypto/rand"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// NewULID returns a new ULID using crypto/rand entropy.
func NewULID() string {
	t := ulid.Timestamp(time.Now())
	id, err := ulid.New(t, cryptorand.Reader)
	if err != nil {
		// crypto/rand should never fail; fall back to zero-time so we still
		// return something usable rather than panicking the CLI.
		id = ulid.MustNew(t, ulid.Monotonic(cryptorand.Reader, 0))
	}
	return id.String()
}

// NewProjectID returns a P-prefixed ULID.
func NewProjectID() string { return "P-" + NewULID() }

// NewInvestigationID returns an I-prefixed ULID.
func NewInvestigationID() string { return "I-" + NewULID() }

// NewAnnotationID returns an A-prefixed ULID.
func NewAnnotationID() string { return "A-" + NewULID() }

// NewRunID returns a RUN-prefixed ULID.
func NewRunID() string { return "RUN-" + NewULID() }

// NewFreezeID returns an F-prefixed ULID.
func NewFreezeID() string { return "F-" + NewULID() }

// NewWorkbenchID returns a WB-prefixed ULID for workbench sessions.
func NewWorkbenchID() string { return "WB-" + NewULID() }

// LooksLikeULID reports whether s appears to be a (possibly prefixed) ULID
// rather than a slug. Heuristic: contains uppercase chars after any "X-"
// prefix and length ≥ 16. Slugs are all-lowercase + digits + dashes.
func LooksLikeULID(s string) bool {
	stripped := s
	if i := strings.Index(s, "-"); i >= 0 && i <= 4 {
		stripped = s[i+1:]
	}
	if len(stripped) < 8 {
		return false
	}
	for _, r := range stripped {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}
