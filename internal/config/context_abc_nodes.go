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

// AbcNodesNomadNamespaceForCLI returns NOMAD_NAMESPACE to inject for Nomad CLI
// passthrough when cluster_type is abc-nodes, admin.abc_nodes.nomad_namespace is set,
// and the operator has not already exported NOMAD_NAMESPACE.
func (c Context) AbcNodesNomadNamespaceForCLI() string {
	if !c.IsABCNodesCluster() {
		return ""
	}
	n := c.abcNodes()
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.NomadNamespace)
}

// AbcNodesNomadNamespaceOrDefault returns admin.abc_nodes.nomad_namespace when set, else "default".
func (c Context) AbcNodesNomadNamespaceOrDefault() string {
	n := c.abcNodes()
	if n == nil {
		return "default"
	}
	if v := strings.TrimSpace(n.NomadNamespace); v != "" {
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

func (c Context) abcNodesSharedStorageEnv(s3Endpoint string) map[string]string {
	if !c.IsABCNodesCluster() {
		return nil
	}
	n := c.abcNodes()
	if n == nil {
		return nil
	}
	ak := strings.TrimSpace(n.S3AccessKey)
	sk := strings.TrimSpace(n.S3SecretKey)
	if ak == "" {
		ak = strings.TrimSpace(n.MinioRootUser)
	}
	if sk == "" {
		sk = strings.TrimSpace(n.MinioRootPassword)
	}
	out := make(map[string]string)
	if ak != "" {
		out["AWS_ACCESS_KEY_ID"] = ak
	}
	if sk != "" {
		out["AWS_SECRET_ACCESS_KEY"] = sk
	}
	if r := strings.TrimSpace(n.S3Region); r != "" {
		out["AWS_DEFAULT_REGION"] = r
	}
	if ep := strings.TrimSpace(s3Endpoint); ep != "" {
		out["AWS_ENDPOINT_URL"] = ep
		out["AWS_ENDPOINT_URL_S3"] = ep
	}
	mu := strings.TrimSpace(n.MinioRootUser)
	mp := strings.TrimSpace(n.MinioRootPassword)
	if mu != "" {
		out["MINIO_ROOT_USER"] = mu
	}
	if mp != "" {
		out["MINIO_ROOT_PASSWORD"] = mp
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AbcNodesMinioStorageCLIEnv returns environment variables for mc / mcli when cluster_type
// is abc-nodes: credentials from admin.abc_nodes, S3 API base URL from admin.services.minio.endpoint
// (legacy admin.abc_nodes.s3_endpoint is still honored until config is saved after migration).
func (c Context) AbcNodesMinioStorageCLIEnv() map[string]string {
	return c.abcNodesSharedStorageEnv(c.minioS3APIEndpoint())
}

// AbcNodesRustfsStorageCLIEnv returns environment variables for the rustfs CLI when cluster_type
// is abc-nodes: same credentials as MinIO helpers, S3 API base URL from admin.services.rustfs.endpoint only.
func (c Context) AbcNodesRustfsStorageCLIEnv() map[string]string {
	return c.abcNodesSharedStorageEnv(c.rustfsS3APIEndpoint())
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
