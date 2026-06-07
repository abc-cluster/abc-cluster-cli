package appgen

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultSpecFile is the descriptor `abc app deploy` reads when -f is not given.
const DefaultSpecFile = "abc-app.yaml"

// Load reads and parses an abc-app.yaml from path. It does NOT validate or
// apply defaults — callers run Validate() then ApplyDefaults(). A malformed
// file produces an error that identifies the field/line where possible (yaml.v3
// embeds `line N` in its type errors).
func Load(path string) (*Spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no app descriptor at %s — create an abc-app.yaml or pass -f/--file", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse decodes abc-app.yaml bytes into a Spec with strict (KnownFields)
// decoding so a typo'd or unsupported key is reported rather than silently
// dropped. yaml.v3 includes the source line in its error text.
func Parse(raw []byte) (*Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parse abc-app.yaml: %w", err)
	}
	return &s, nil
}
