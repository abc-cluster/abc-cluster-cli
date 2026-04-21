package config

import "strings"

// IsABCNodesCluster reports whether this context is treated as the abc-nodes tier
// (including legacy cluster_type "abc-node").
func (c Context) IsABCNodesCluster() bool {
	norm, ok := NormalizeClusterType(c.ClusterType)
	return ok && norm == ClusterTypeABCNodes
}

func (c Context) abcNodes() *AdminABCNodes {
	if c.Admin.ABCNodes == nil {
		return nil
	}
	return c.Admin.ABCNodes
}

// ABCNodes returns admin.abc_nodes for this context, or nil.
func (c Context) ABCNodes() *AdminABCNodes { return c.abcNodes() }

// MinioS3APIEndpoint returns admin.services.minio.endpoint, or legacy admin.abc_nodes.s3_endpoint.
func (c Context) MinioS3APIEndpoint() string { return c.minioS3APIEndpoint() }

// RustfsS3APIEndpoint returns admin.services.rustfs.endpoint.
func (c Context) RustfsS3APIEndpoint() string { return c.rustfsS3APIEndpoint() }

// Suffixes longest-first so e.g. "_researcher" wins over a hypothetical shorter overlap.
var adminWhoamiNomadNamespaceSuffixes = []string{
	"_researcher",
	"_submit",
	"_member",
	"_admin",
	"_user",
}

// deriveNomadNamespaceFromColonPersona handles admin/auth whoami strings shaped like
// "<realm>:<nomad-namespace>:<role>" (e.g. aither:su-mbhg-bioinformatics:researcher).
// Returns the middle segment when it looks like a Nomad namespace slug.
func deriveNomadNamespaceFromColonPersona(whoami string) string {
	w := strings.TrimSpace(whoami)
	if w == "" || strings.Contains(w, "//") {
		return ""
	}
	parts := strings.Split(w, ":")
	if len(parts) < 3 {
		return ""
	}
	ns := strings.TrimSpace(parts[1])
	if ns == "" {
		return ""
	}
	return ns
}

// deriveNomadNamespaceFromAdminWhoami returns the Nomad namespace implied by admin.whoami
// when it uses a known trailing _<role> pattern. Empty string if none match.
func deriveNomadNamespaceFromAdminWhoami(whoami string) string {
	w := strings.TrimSpace(whoami)
	if w == "" {
		return ""
	}
	if ns := deriveNomadNamespaceFromColonPersona(w); ns != "" {
		return ns
	}
	for _, suf := range adminWhoamiNomadNamespaceSuffixes {
		if strings.HasSuffix(w, suf) {
			ns := strings.TrimSpace(strings.TrimSuffix(w, suf))
			if ns != "" {
				return ns
			}
		}
	}
	return ""
}

func (c Context) whoamiForNomadNamespaceDerivation() string {
	if v := strings.TrimSpace(c.Admin.Whoami); v != "" {
		return v
	}
	if c.Auth != nil {
		return strings.TrimSpace(c.Auth.Whoami)
	}
	return ""
}

func (c Context) resolvedAbcNodesNomadNamespace() string {
	if n := c.abcNodes(); n != nil {
		if v := strings.TrimSpace(n.NomadNamespace); v != "" {
			return v
		}
	}
	if !c.IsABCNodesCluster() {
		return ""
	}
	return deriveNomadNamespaceFromAdminWhoami(c.whoamiForNomadNamespaceDerivation())
}

// AbcNodesNomadNamespaceForCLI returns NOMAD_NAMESPACE to inject for Nomad CLI
// passthrough when cluster_type is abc-nodes and a namespace is resolved from
// admin.abc_nodes.nomad_namespace, admin.whoami, or auth.whoami, and the operator has not
// already exported NOMAD_NAMESPACE.
func (c Context) AbcNodesNomadNamespaceForCLI() string {
	if !c.IsABCNodesCluster() {
		return ""
	}
	return c.resolvedAbcNodesNomadNamespace()
}

// AbcNodesNomadNamespaceOrDefault returns the resolved Nomad namespace for abc-nodes
// (explicit admin.abc_nodes.nomad_namespace, else derived from admin/auth whoami), else "default".
func (c Context) AbcNodesNomadNamespaceOrDefault() string {
	if v := c.resolvedAbcNodesNomadNamespace(); v != "" {
		return v
	}
	return "default"
}

// minioS3APIEndpoint returns admin.services.minio.endpoint, or legacy admin.abc_nodes.s3_endpoint
// when the context was not loaded through config.Load (tests, in-memory).
func (c Context) minioS3APIEndpoint() string {
	if v, ok := GetAdminFloorField(&c.Admin.Services, "minio", "endpoint"); ok {
		return v
	}
	if n := c.abcNodes(); n != nil {
		return strings.TrimSpace(n.S3Endpoint)
	}
	return ""
}

// rustfsS3APIEndpoint returns admin.services.rustfs.endpoint.
func (c Context) rustfsS3APIEndpoint() string {
	v, ok := GetAdminFloorField(&c.Admin.Services, "rustfs", "endpoint")
	if !ok {
		return ""
	}
	return v
}

// svc is "minio" or "rustfs" — selects admin.services.<svc>.access_key / secret_key before abc_nodes.
func (c Context) abcNodesSharedStorageEnv(s3Endpoint string, svc string) map[string]string {
	if !c.IsABCNodesCluster() {
		return nil
	}
	var ak, sk string
	switch svc {
	case "minio":
		if v, ok := GetAdminFloorField(&c.Admin.Services, "minio", "access_key"); ok {
			ak = v
		}
		if v, ok := GetAdminFloorField(&c.Admin.Services, "minio", "secret_key"); ok {
			sk = v
		}
	case "rustfs":
		if v, ok := GetAdminFloorField(&c.Admin.Services, "rustfs", "access_key"); ok {
			ak = v
		}
		if v, ok := GetAdminFloorField(&c.Admin.Services, "rustfs", "secret_key"); ok {
			sk = v
		}
	}
	n := c.abcNodes()
	if ak == "" || sk == "" {
		if n != nil {
			if ak == "" {
				ak = strings.TrimSpace(n.S3AccessKey)
			}
			if sk == "" {
				sk = strings.TrimSpace(n.S3SecretKey)
			}
			if ak == "" {
				ak = strings.TrimSpace(n.MinioRootUser)
			}
			if sk == "" {
				sk = strings.TrimSpace(n.MinioRootPassword)
			}
		}
	}
	if n == nil && ak == "" && sk == "" && strings.TrimSpace(s3Endpoint) == "" {
		return nil
	}
	out := make(map[string]string)
	if ak != "" {
		out["AWS_ACCESS_KEY_ID"] = ak
	}
	if sk != "" {
		out["AWS_SECRET_ACCESS_KEY"] = sk
	}
	if n != nil {
		if r := strings.TrimSpace(n.S3Region); r != "" {
			out["AWS_DEFAULT_REGION"] = r
		}
	}
	if ep := strings.TrimSpace(s3Endpoint); ep != "" {
		out["AWS_ENDPOINT_URL"] = ep
		out["AWS_ENDPOINT_URL_S3"] = ep
	}
	if n != nil {
		mu := strings.TrimSpace(n.MinioRootUser)
		mp := strings.TrimSpace(n.MinioRootPassword)
		if mu != "" {
			out["MINIO_ROOT_USER"] = mu
		}
		if mp != "" {
			out["MINIO_ROOT_PASSWORD"] = mp
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AbcNodesMinioStorageCLIEnv returns environment variables for mc / mcli when cluster_type
// is abc-nodes: credentials from admin.services.minio.access_key / secret_key when set, else
// admin.abc_nodes; S3 API base URL from admin.services.minio.endpoint (legacy s3_endpoint in memory).
func (c Context) AbcNodesMinioStorageCLIEnv() map[string]string {
	return c.abcNodesSharedStorageEnv(c.minioS3APIEndpoint(), "minio")
}

// AbcNodesRustfsStorageCLIEnv returns environment variables for the rustfs CLI when cluster_type
// is abc-nodes: credentials from admin.services.rustfs.access_key / secret_key when set, else
// admin.abc_nodes; S3 API base URL from admin.services.rustfs.endpoint only.
func (c Context) AbcNodesRustfsStorageCLIEnv() map[string]string {
	return c.abcNodesSharedStorageEnv(c.rustfsS3APIEndpoint(), "rustfs")
}

// AbcNodesVaultCLIEnv returns environment variables for vault / bao / openbao CLIs when cluster_type
// is abc-nodes: VAULT_ADDR from admin.services.vault.http (no trailing slash), optional
// VAULT_TOKEN from admin.services.vault.access_key (lab dev root token), only for keys not
// already set in the process environment.
func (c Context) AbcNodesVaultCLIEnv() map[string]string {
	if !c.IsABCNodesCluster() {
		return nil
	}
	addr, ok := GetAdminFloorField(&c.Admin.Services, "vault", "http")
	if !ok {
		return nil
	}
	addr = strings.TrimSpace(addr)
	addr = strings.TrimSuffix(addr, "/")
	if addr == "" {
		return nil
	}
	out := map[string]string{"VAULT_ADDR": addr}
	if tok, ok := GetAdminFloorField(&c.Admin.Services, "vault", "access_key"); ok {
		if t := strings.TrimSpace(tok); t != "" {
			out["VAULT_TOKEN"] = t
		}
	}
	return out
}

// migrateAbcNodesLegacyS3Endpoint copies deprecated admin.abc_nodes.s3_endpoint into
// admin.services.minio.endpoint when the latter is empty, then clears the legacy field.
func migrateAbcNodesLegacyS3Endpoint(ctx *Context) {
	n := ctx.abcNodes()
	if n == nil {
		return
	}
	ep := strings.TrimSpace(n.S3Endpoint)
	if ep == "" {
		return
	}
	if existing, ok := GetAdminFloorField(&ctx.Admin.Services, "minio", "endpoint"); ok && strings.TrimSpace(existing) != "" {
		n.S3Endpoint = ""
		return
	}
	_ = SetAdminFloorField(&ctx.Admin.Services, "minio", "endpoint", ep)
	n.S3Endpoint = ""
	NormalizeFloorServices(ctx)
}

// normalizeContextAbcNodes clears an empty admin.abc_nodes block after load.
func normalizeContextAbcNodes(ctx *Context) {
	n := ctx.Admin.ABCNodes
	if n == nil {
		return
	}
	if strings.TrimSpace(n.NomadNamespace) == "" &&
		strings.TrimSpace(n.S3AccessKey) == "" &&
		strings.TrimSpace(n.S3SecretKey) == "" &&
		strings.TrimSpace(n.S3Region) == "" &&
		strings.TrimSpace(n.S3Endpoint) == "" &&
		strings.TrimSpace(n.MinioRootUser) == "" &&
		strings.TrimSpace(n.MinioRootPassword) == "" {
		ctx.Admin.ABCNodes = nil
	}
}
