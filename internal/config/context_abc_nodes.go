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

// AbcNodesStorageCLIEnv returns environment variables for mc / rustfs / other
// S3-aware CLIs. Keys are only present for non-empty resolved values.
// Precedence for access/secret: s3_access_key / s3_secret_key, else minio_root_*.
// Only applies when IsABCNodesCluster is true.
func (c Context) AbcNodesStorageCLIEnv() map[string]string {
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
	if ep := strings.TrimSpace(n.S3Endpoint); ep != "" {
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
