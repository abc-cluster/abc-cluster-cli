// Package secretref builds the Nomad template references that let a job
// allocation read an `abc secrets` value at runtime, so the plaintext never
// passes through the CLI or lands in the submitted job definition.
//
// Path conventions (shared with cmd/secrets):
//
//	nomad: abc/secrets/<namespace>/<name>           item "value"
//	vault: secret/data/abc/<namespace>/<name>       field "value"
package secretref

import (
	"fmt"
	"regexp"
	"strings"
)

// URIPrefix marks a value as a reference to an abc secret: secret://<name>.
const URIPrefix = "secret://"

// NomadPrefix is the Nomad Variables path prefix abc secrets live under.
const NomadPrefix = "abc/secrets"

// Backend names.
const (
	BackendNomad = "nomad"
	BackendVault = "vault"
)

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// BackendFor maps a context's capabilities.secrets value to the backend whose
// template syntax a job must use. Anything other than vault means nomad.
func BackendFor(capsSecrets string) string {
	switch strings.TrimSpace(capsSecrets) {
	case "vault", "vault+sealed":
		return BackendVault
	default:
		return BackendNomad
	}
}

// ParseURI reports whether s is a secret://<name> reference and returns the
// trimmed name.
func ParseURI(s string) (name string, ok bool) {
	name, ok = strings.CutPrefix(strings.TrimSpace(s), URIPrefix)
	if !ok {
		return "", false
	}
	name = strings.TrimSpace(name)
	return name, name != ""
}

// ValidateName rejects names that cannot form a single Nomad Variables path
// segment (no slashes, no template delimiters, no whitespace).
func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid secret name %q: use letters, digits, '.', '_' or '-' (must start with a letter or digit)", name)
	}
	return nil
}

// NomadPath is the Nomad Variables path of a secret.
func NomadPath(namespace, name string) string {
	return NomadPrefix + "/" + nsOrDefault(namespace) + "/" + name
}

// NomadPolicyPathGlob is the Nomad ACL variables path glob covering every
// secret in a namespace.
func NomadPolicyPathGlob(namespace string) string {
	return NomadPrefix + "/" + nsOrDefault(namespace) + "/*"
}

// TemplateRef returns the Nomad template expression that renders the
// secret's value at alloc start.
func TemplateRef(backend, namespace, name string) string {
	ns := nsOrDefault(namespace)
	if backend == BackendVault {
		return fmt.Sprintf(`{{ with secret "secret/data/abc/%s/%s" }}{{ .Data.data.value }}{{ end }}`, ns, name)
	}
	return fmt.Sprintf(`{{ with nomadVar %q }}{{ index . "value" }}{{ end }}`, NomadPath(ns, name))
}

// TemplateRefJSON is TemplateRef with the value rendered as a JSON string
// literal ("…" with escapes). Nomad parses `env = true` template output with
// go-envparse, which accepts double-quoted JSON escapes, so this survives
// spaces, '#', quotes and newlines that would corrupt an unquoted KEY=value
// line. `printf "%s"` first turns the nomadVar item into its plain string
// (toJSON on the item itself would serialise the struct).
func TemplateRefJSON(backend, namespace, name string) string {
	ns := nsOrDefault(namespace)
	if backend == BackendVault {
		return fmt.Sprintf(`{{ with secret "secret/data/abc/%s/%s" }}{{ .Data.data.value | printf "%%s" | toJSON }}{{ end }}`, ns, name)
	}
	return fmt.Sprintf(`{{ with nomadVar %q }}{{ index . "value" | printf "%%s" | toJSON }}{{ end }}`, NomadPath(ns, name))
}

func nsOrDefault(ns string) string {
	if ns = strings.TrimSpace(ns); ns == "" {
		return "default"
	}
	return ns
}
