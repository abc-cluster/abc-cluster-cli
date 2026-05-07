// Package slug generates human-friendly slugs of the form <adj>-<noun>-<n>.
package slug

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"regexp"
)

// SlugPattern is the validation regex for user-supplied slugs.
const SlugPattern = `^[a-z][a-z0-9-]{2,40}$`

var slugRegex = regexp.MustCompile(SlugPattern)

// Validate reports whether s is a syntactically valid slug.
func Validate(s string) error {
	if !slugRegex.MatchString(s) {
		return fmt.Errorf("slug %q does not match %s", s, SlugPattern)
	}
	return nil
}

// Generate produces a random slug. Format: <adjective>-<noun>-<1..99>.
func Generate() string {
	adj := pick(Adjectives)
	noun := pick(Nouns)
	n := pickInt(99) + 1
	return fmt.Sprintf("%s-%s-%d", adj, noun, n)
}

// CollisionFn returns true if the slug already exists in the relevant scope.
type CollisionFn func(slug string) (bool, error)

// GenerateUnique returns a slug not present according to fn. Up to maxRetries
// attempts; afterwards returns ErrExhausted.
func GenerateUnique(fn CollisionFn, maxRetries int) (string, error) {
	if maxRetries <= 0 {
		maxRetries = 5
	}
	var last string
	for i := 0; i < maxRetries; i++ {
		s := Generate()
		exists, err := fn(s)
		if err != nil {
			return "", err
		}
		if !exists {
			return s, nil
		}
		last = s
	}
	return "", fmt.Errorf("%w (last attempt: %s)", ErrExhausted, last)
}

// ErrExhausted is returned by GenerateUnique when all attempts collided.
var ErrExhausted = errors.New("slug collision retries exhausted")

func pick(s []string) string {
	idx := pickInt(len(s))
	return s[idx]
}

// pickInt returns a uniformly random int in [0, n).
func pickInt(n int) int {
	if n <= 0 {
		return 0
	}
	bn, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		// Cryptographic RNG failure is exceptional; fall back to a
		// deterministic-but-non-zero index from a few extra reads.
		var b [8]byte
		_, _ = rand.Read(b[:])
		return int(binary.BigEndian.Uint64(b[:]) % uint64(n))
	}
	return int(bn.Int64())
}
