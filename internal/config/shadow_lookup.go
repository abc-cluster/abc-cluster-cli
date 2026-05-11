// shadow_lookup.go — local-only (no remote API) field lookups for
// `abc admin env show` and `abc admin env validate` shadowing
// introspection.
//
// For actual cred-source resolution (which CAN fetch from Nomad
// Variables and Vault KV2), use cmd/utils.ResolveAdminFloorField. The
// helpers here intentionally do NOT resolve nomad+var@ / vault+kv2@
// references — they return the raw reference strings so the
// introspection commands can show "this is what would be fetched"
// without paying the network round-trip on every `abc admin env show`.

package config

import "strings"

// AdminFloorFieldValue returns the raw config value for one
// (service, selection, key) tuple from the active context. Returns
// the empty string when no value is configured.
//
//   - For selection="local": the literal value from
//     cred_source.local.<key>, falling back to the top-level inline
//     field (svc.<Key>) when cred_source.local doesn't define it.
//   - For selection="nomad": the reference string from
//     cred_source.nomad.<key> (typically "nomad+var@<ns>/<path>#<key>").
//     NOT resolved.
//   - For selection="vault": the reference string from
//     cred_source.vault.<key> (typically "vault+kv2@<mount>/data/<path>#<key>").
//     NOT resolved.
//
// Returns ("", false) when the field is not configured for the given
// (service, selection, key).
func AdminFloorFieldValue(ctx Context, svcName, selection, key string) (string, bool) {
	svc := AdminFloorServiceNamed(&ctx.Admin.Services, svcName)
	if svc == nil {
		return "", false
	}
	sel := strings.TrimSpace(strings.ToLower(selection))

	if svc.CredSource != nil {
		var m map[string]string
		switch sel {
		case "local":
			m = svc.CredSource.Local
		case "nomad":
			m = svc.CredSource.Nomad
		case "vault":
			m = svc.CredSource.Vault
		}
		if v, ok := m[key]; ok && strings.TrimSpace(v) != "" {
			return v, true
		}
	}

	// Local fallback: top-level inline field on the service struct.
	if sel == "local" {
		v := adminFloorInlineField(svc, key)
		if strings.TrimSpace(v) != "" {
			return v, true
		}
		// Last-resort: admin.abc_nodes.{minio_root_user,minio_root_password}
		// for MinIO when no other source has the value.
		if svcName == "minio" {
			if n := ctx.ABCNodes(); n != nil {
				switch key {
				case "user":
					if v := strings.TrimSpace(n.MinioRootUser); v != "" {
						return v, true
					}
				case "password":
					if v := strings.TrimSpace(n.MinioRootPassword); v != "" {
						return v, true
					}
				case "access_key":
					if v := strings.TrimSpace(n.S3AccessKey); v != "" {
						return v, true
					}
					if v := strings.TrimSpace(n.MinioRootUser); v != "" {
						return v, true
					}
				case "secret_key":
					if v := strings.TrimSpace(n.S3SecretKey); v != "" {
						return v, true
					}
					if v := strings.TrimSpace(n.MinioRootPassword); v != "" {
						return v, true
					}
				}
			}
		}
	}

	return "", false
}

// ContextDirectFieldValue returns the value at a direct context path
// (no admin-floor indirection). Supported paths:
//
//	"crypt.password"   → ctx.Crypt.Password
//	"crypt.salt"       → ctx.Crypt.Salt
//	"upload_endpoint"  → ctx.UploadEndpoint
//	"upload_token"     → ctx.UploadToken
//
// Returns ("", false) for unknown paths or empty values.
func ContextDirectFieldValue(ctx Context, path string) (string, bool) {
	var v string
	switch strings.TrimSpace(strings.ToLower(path)) {
	case "crypt.password":
		v = ctx.Crypt.Password
	case "crypt.salt":
		v = ctx.Crypt.Salt
	case "upload_endpoint":
		v = ctx.UploadEndpoint
	case "upload_token":
		v = ctx.UploadToken
	default:
		return "", false
	}
	if strings.TrimSpace(v) == "" {
		return "", false
	}
	return v, true
}

// IsReferenceString reports whether v looks like a nomad+var@ or
// vault+kv2@ cred-source reference (rather than a literal value).
// Used by `abc admin env show` to label the value source.
func IsReferenceString(v string) bool {
	s := strings.TrimSpace(v)
	return strings.HasPrefix(s, "nomad+var@") ||
		strings.HasPrefix(s, "vault+kv2@")
}

// adminFloorInlineField returns the top-level inline field on the
// service struct (the value that lives outside cred_source). Used as
// the local-selection fallback.
func adminFloorInlineField(svc *AdminFloorService, key string) string {
	if svc == nil {
		return ""
	}
	switch strings.TrimSpace(strings.ToLower(key)) {
	case "http":
		return svc.HTTP
	case "endpoint":
		return svc.Endpoint
	case "access_key":
		return svc.AccessKey
	case "secret_key":
		return svc.SecretKey
	case "user":
		return svc.User
	case "password":
		return svc.Password
	}
	return ""
}
