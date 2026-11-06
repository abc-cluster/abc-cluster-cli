package job

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	jobhcl "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/job"
	"github.com/abc-cluster/abc-cluster-cli/internal/secretref"
)

// Secret directives:
//
//	#ABC --secret=ENV_VAR=<name>          export the secret as $ENV_VAR
//	#ABC --secret-file=<name>             write it to $NOMAD_SECRETS_DIR/<name>
//	#ABC --secret-file=<file>=<name>      write it to $NOMAD_SECRETS_DIR/<file>
//
// <name> may also be written secret://<name>. The CLI flags --secret and
// --secret-file take the same values. Values are rendered by Nomad templates
// at alloc start from abc secrets (abc/secrets/<namespace>/<name>), so the
// plaintext never appears in the submitted job definition.

// secretEnvFile is where env-mode secrets render (in-memory, noexec dir).
const secretEnvFile = "secrets/abc-secrets.env"

var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var secretFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func secretName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if n, ok := secretref.ParseURI(name); ok {
		name = n
	}
	if err := secretref.ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}

// parseSecretEnv parses ENV_VAR=<name> into spec.SecretEnv.
func parseSecretEnv(spec *jobSpec, val, source string) error {
	env, ref, ok := strings.Cut(val, "=")
	env = strings.TrimSpace(env)
	if !ok || env == "" || strings.TrimSpace(ref) == "" {
		return fmt.Errorf("%s --secret requires ENV_VAR=<secret-name>, got %q", source, val)
	}
	if !envVarName.MatchString(env) {
		return fmt.Errorf("%s --secret: invalid env var name %q", source, env)
	}
	name, err := secretName(ref)
	if err != nil {
		return fmt.Errorf("%s --secret %s: %w", source, env, err)
	}
	if spec.SecretEnv == nil {
		spec.SecretEnv = map[string]string{}
	}
	spec.SecretEnv[env] = name
	return nil
}

// parseSecretFile parses [<file>=]<name> into spec.SecretFiles.
func parseSecretFile(spec *jobSpec, val, source string) error {
	val = strings.TrimSpace(val)
	if val == "" {
		return fmt.Errorf("%s --secret-file requires [<file>=]<secret-name>", source)
	}
	file, ref, hasFile := val, val, false
	// Split on the first '=' only when it precedes any "secret://" (which has
	// no '=' itself, so a bare reference never splits).
	if f, r, ok := strings.Cut(val, "="); ok {
		file, ref, hasFile = strings.TrimSpace(f), r, true
	}
	name, err := secretName(ref)
	if err != nil {
		return fmt.Errorf("%s --secret-file: %w", source, err)
	}
	if !hasFile {
		file = name
	}
	if !secretFileName.MatchString(file) {
		return fmt.Errorf("%s --secret-file: invalid file name %q (a single name under $NOMAD_SECRETS_DIR; no '/')", source, file)
	}
	if file == "abc-secrets.env" {
		return fmt.Errorf("%s --secret-file: %q is reserved for --secret env vars", source, file)
	}
	if spec.SecretFiles == nil {
		spec.SecretFiles = map[string]string{}
	}
	spec.SecretFiles[file] = name
	return nil
}

// secretTemplates renders spec's secret bindings as Nomad template stanzas.
func secretTemplates(spec *jobSpec, backend string) []jobhcl.TemplateSpec {
	if len(spec.SecretEnv) == 0 && len(spec.SecretFiles) == 0 {
		return nil
	}
	ns := spec.Namespace
	var out []jobhcl.TemplateSpec
	if len(spec.SecretEnv) > 0 {
		var b strings.Builder
		for _, env := range sortedKeys(spec.SecretEnv) {
			fmt.Fprintf(&b, "%s=%s\n", env, secretref.TemplateRefJSON(backend, ns, spec.SecretEnv[env]))
		}
		out = append(out, jobhcl.TemplateSpec{
			Data:        b.String(),
			Destination: secretEnvFile,
			Perms:       "0400",
			Env:         true,
			ChangeMode:  "noop",
		})
	}
	for _, file := range sortedKeys(spec.SecretFiles) {
		out = append(out, jobhcl.TemplateSpec{
			Data:        secretref.TemplateRef(backend, ns, spec.SecretFiles[file]),
			Destination: "secrets/" + file,
			Perms:       "0400",
			ChangeMode:  "noop",
		})
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// validateSecretDriver rejects secret bindings on drivers whose generated job
// carries no Nomad template stanzas (the script is passed inline), where the
// secret would otherwise be silently dropped.
func validateSecretDriver(spec *jobSpec) error {
	if len(spec.SecretEnv) == 0 && len(spec.SecretFiles) == 0 {
		return nil
	}
	switch spec.Driver {
	case "slurm", "pbs":
		return fmt.Errorf("--secret/--secret-file are not supported with --driver=%s (no Nomad templates are rendered for it)", spec.Driver)
	}
	return nil
}

// secretsBackendFromConfig picks the template syntax from the active
// context's synced capabilities.secrets (nomad unless it says vault).
func secretsBackendFromConfig() string {
	c, err := cfg.Load()
	if err != nil || c == nil {
		return secretref.BackendNomad
	}
	if caps := c.ActiveCtx().Capabilities; caps != nil {
		return secretref.BackendFor(caps.Secrets)
	}
	return secretref.BackendNomad
}
