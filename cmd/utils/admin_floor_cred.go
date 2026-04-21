package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// Cred config branch values for admin.services.<svc>.cred_source and --config.
const (
	CredConfigLocal = "local"
	CredConfigNomad = "nomad"
	CredConfigVault = "vault"
)

// ParseAdminServiceCLIArgs parses optional leading --binary-location and --config
// from argv for admin service CLI passthrough commands.
// If allowVaultSelection is false, --config may only be local or nomad (used for
// admin.services.vault, where cred_source.vault is not supported).
func ParseAdminServiceCLIArgs(args []string, allowVaultSelection bool) (cfgSelection, binaryLocation string, passthrough []string, err error) {
	cfgSelection = CredConfigLocal
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			return cfgSelection, binaryLocation, append([]string(nil), args[i+1:]...), nil
		}
		switch {
		case a == "--binary-location":
			if i+1 >= len(args) {
				return "", "", nil, fmt.Errorf("--binary-location requires a value")
			}
			binaryLocation = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--binary-location="):
			binaryLocation = strings.TrimPrefix(a, "--binary-location=")
			i++
		case a == "--config":
			if i+1 >= len(args) {
				return "", "", nil, fmt.Errorf("--config requires a value")
			}
			cfgSelection = strings.TrimSpace(strings.ToLower(args[i+1]))
			i += 2
		case strings.HasPrefix(a, "--config="):
			cfgSelection = strings.TrimSpace(strings.ToLower(strings.TrimPrefix(a, "--config=")))
			i++
		default:
			if err := validateCredConfigSelection(cfgSelection, allowVaultSelection); err != nil {
				return "", "", nil, err
			}
			return cfgSelection, binaryLocation, append([]string(nil), args[i:]...), nil
		}
	}
	if err := validateCredConfigSelection(cfgSelection, allowVaultSelection); err != nil {
		return "", "", nil, err
	}
	return cfgSelection, binaryLocation, nil, nil
}

func validateCredConfigSelection(s string, allowVault bool) error {
	switch s {
	case CredConfigLocal, CredConfigNomad:
		return nil
	case CredConfigVault:
		if !allowVault {
			return fmt.Errorf("invalid --config %q (this service only supports local|nomad)", s)
		}
		return nil
	default:
		if allowVault {
			return fmt.Errorf("invalid --config %q (expected local|nomad|vault)", s)
		}
		return fmt.Errorf("invalid --config %q (expected local|nomad)", s)
	}
}

// VaultFloorNoVaultBackend marks admin.services.vault: cred_source.vault is ignored
// and vault+kv2 references must not be resolved from that branch.
func VaultFloorNoVaultBackend(svcName string) bool {
	return svcName == "vault"
}

// ResolveAdminFloorField resolves one field for an admin floor service using
// cred_source and top-level fields. selection is local|nomad|vault.
func ResolveAdminFloorField(ctx context.Context, c config.Context, svc *config.AdminFloorService, svcName, selection, key string) (string, error) {
	vaultFloor := VaultFloorNoVaultBackend(svcName)
	top := strings.TrimSpace(adminFloorStructField(svc, key))
	local := strings.TrimSpace(credSourceMapValue(svc, svcName, CredConfigLocal, key))

	if selection == CredConfigLocal {
		if local != "" {
			return local, nil
		}
		return top, nil
	}

	if vaultFloor && selection == CredConfigVault {
		return "", fmt.Errorf("admin.services.vault does not support --config vault")
	}

	ref := strings.TrimSpace(credSourceMapValue(svc, svcName, selection, key))
	if ref != "" {
		v, err := resolveCredentialReference(ctx, c, selection, ref)
		if err != nil {
			return "", fmt.Errorf("resolve admin.services.%s.cred_source.%s.%s: %w", svcName, selection, key, err)
		}
		return strings.TrimSpace(v), nil
	}
	if local != "" {
		return local, nil
	}
	return top, nil
}

func adminFloorStructField(svc *config.AdminFloorService, key string) string {
	if svc == nil {
		return ""
	}
	switch key {
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
	case "ping_entrypoint":
		return svc.PingEntryPoint
	case "dashboard":
		return svc.Dashboard
	default:
		return ""
	}
}

func credSourceMapValue(svc *config.AdminFloorService, svcName, selection, key string) string {
	if svc == nil || svc.CredSource == nil {
		return ""
	}
	if VaultFloorNoVaultBackend(svcName) && selection == CredConfigVault {
		return ""
	}
	var m map[string]string
	switch selection {
	case CredConfigLocal:
		m = svc.CredSource.Local
	case CredConfigNomad:
		m = svc.CredSource.Nomad
	case CredConfigVault:
		m = svc.CredSource.Vault
	default:
		return ""
	}
	if m == nil {
		return ""
	}
	return m[key]
}

// resolveCredentialReference resolves nomad+var@ or vault+kv2@ reference strings.
func resolveCredentialReference(ctx context.Context, c config.Context, selection, ref string) (string, error) {
	switch selection {
	case CredConfigNomad:
		return resolveNomadVariableRef(ctx, c, ref)
	case CredConfigVault:
		return resolveVaultKV2Ref(ctx, c, ref)
	default:
		return "", fmt.Errorf("unsupported remote config %q", selection)
	}
}

func resolveNomadVariableRef(ctx context.Context, c config.Context, ref string) (string, error) {
	const prefix = "nomad+var@"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("invalid Nomad ref %q (expected nomad+var@<namespace>/<path>#<key>)", ref)
	}
	raw := strings.TrimPrefix(ref, prefix)
	key := ""
	if i := strings.Index(raw, "#"); i >= 0 {
		key = strings.TrimSpace(raw[i+1:])
		raw = raw[:i]
	}
	raw = strings.TrimSpace(raw)
	slash := strings.Index(raw, "/")
	if slash <= 0 || slash == len(raw)-1 {
		return "", fmt.Errorf("invalid Nomad ref %q (expected nomad+var@<namespace>/<path>#<key>)", ref)
	}
	namespace := raw[:slash]
	path := raw[slash+1:]

	nc := NewNomadClient(c.NomadAddr(), c.NomadToken(), c.NomadRegion())
	variable, err := nc.GetVariable(ctx, path, namespace)
	if err != nil {
		return "", err
	}
	return valueFromStringMap(variable.Items, key)
}

func resolveVaultKV2Ref(_ context.Context, c config.Context, ref string) (string, error) {
	const prefix = "vault+kv2@"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("invalid Vault ref %q (expected vault+kv2@<mount>/data/<path>#<key>)", ref)
	}
	raw := strings.TrimPrefix(ref, prefix)
	key := ""
	if i := strings.Index(raw, "#"); i >= 0 {
		key = strings.TrimSpace(raw[i+1:])
		raw = raw[:i]
	}
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "/data/") {
		return "", fmt.Errorf("invalid Vault ref %q (expected /data/ in path)", ref)
	}
	base := vaultBaseURLFromContext(c)
	tok := vaultTokenFromEnvOrContext(c)
	if tok == "" {
		return "", fmt.Errorf("VAULT_TOKEN is required to resolve vault references")
	}
	u := strings.TrimRight(base, "/") + "/v1/" + strings.TrimLeft(raw, "/")

	code, body, err := vaultDoRaw(http.MethodGet, u, tok)
	if err != nil {
		return "", err
	}
	if code < 200 || code >= 300 {
		return "", fmt.Errorf("vault API %d: %s", code, strings.TrimSpace(string(body)))
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parse vault response: %w", err)
	}
	data, _ := payload["data"].(map[string]interface{})
	inner, _ := data["data"].(map[string]interface{})
	if len(inner) == 0 {
		return "", fmt.Errorf("vault response missing data.data object")
	}
	return valueFromAnyMap(inner, key)
}

func valueFromStringMap(m map[string]string, key string) (string, error) {
	if len(m) == 0 {
		return "", fmt.Errorf("reference target returned no items")
	}
	if key != "" {
		v, ok := m[key]
		if !ok {
			return "", fmt.Errorf("key %q not found in reference target", key)
		}
		return v, nil
	}
	if v, ok := m["value"]; ok {
		return v, nil
	}
	if len(m) == 1 {
		for _, v := range m {
			return v, nil
		}
	}
	return "", fmt.Errorf("reference contains multiple keys; add #<key>")
}

func valueFromAnyMap(m map[string]interface{}, key string) (string, error) {
	if len(m) == 0 {
		return "", fmt.Errorf("reference target returned no data")
	}
	if key != "" {
		v, ok := m[key]
		if !ok {
			return "", fmt.Errorf("key %q not found in reference target", key)
		}
		return fmt.Sprintf("%v", v), nil
	}
	if v, ok := m["value"]; ok {
		return fmt.Sprintf("%v", v), nil
	}
	if len(m) == 1 {
		for _, v := range m {
			return fmt.Sprintf("%v", v), nil
		}
	}
	return "", fmt.Errorf("reference contains multiple keys; add #<key>")
}

func vaultBaseURLFromContext(c config.Context) string {
	if v := strings.TrimSpace(os.Getenv("VAULT_ADDR")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if c.Admin.Services.Vault != nil {
		if v := strings.TrimSpace(c.Admin.Services.Vault.HTTP); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return "http://127.0.0.1:8200"
}

func vaultTokenFromEnvOrContext(c config.Context) string {
	if v := strings.TrimSpace(os.Getenv("VAULT_TOKEN")); v != "" {
		return v
	}
	if c.Admin.Services.Vault != nil {
		if v := strings.TrimSpace(c.Admin.Services.Vault.AccessKey); v != "" {
			return v
		}
	}
	return ""
}

func vaultDoRaw(method, url, token string) (int, []byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(method, url, nil) //nolint:noctx
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Vault-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

// ResolvedAbcNodesStorageCLIEnv builds AWS_* / MINIO_* env for minio or rustfs CLIs
// using cred_source resolution and admin.abc_nodes fallbacks (abc-nodes only).
func ResolvedAbcNodesStorageCLIEnv(ctx context.Context, cfg *config.Config, svcName, selection string) (map[string]string, error) {
	c := cfg.ActiveCtx()
	if !c.IsABCNodesCluster() {
		return nil, nil
	}
	svc := config.AdminFloorServiceNamed(&c.Admin.Services, svcName)
	if svc == nil && c.ABCNodes() == nil {
		return nil, nil
	}

	get := func(key string) (string, error) {
		return ResolveAdminFloorField(ctx, c, svc, svcName, selection, key)
	}

	endpoint, err := get("endpoint")
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		if svcName == "minio" {
			endpoint = c.MinioS3APIEndpoint()
		} else if svcName == "rustfs" {
			endpoint = c.RustfsS3APIEndpoint()
		}
	}

	accessKey, err := get("access_key")
	if err != nil {
		return nil, err
	}
	secretKey, err := get("secret_key")
	if err != nil {
		return nil, err
	}
	user, err := get("user")
	if err != nil {
		return nil, err
	}
	password, err := get("password")
	if err != nil {
		return nil, err
	}

	if svcName == "minio" {
		if accessKey == "" {
			accessKey = user
		}
		if secretKey == "" {
			secretKey = password
		}
	}

	n := c.ABCNodes()
	if accessKey == "" || secretKey == "" {
		if n != nil {
			if accessKey == "" {
				accessKey = strings.TrimSpace(n.S3AccessKey)
			}
			if secretKey == "" {
				secretKey = strings.TrimSpace(n.S3SecretKey)
			}
			if accessKey == "" {
				accessKey = strings.TrimSpace(n.MinioRootUser)
			}
			if secretKey == "" {
				secretKey = strings.TrimSpace(n.MinioRootPassword)
			}
		}
	}

	if n == nil && accessKey == "" && secretKey == "" && strings.TrimSpace(endpoint) == "" {
		return nil, nil
	}

	out := make(map[string]string)
	if accessKey != "" {
		out["AWS_ACCESS_KEY_ID"] = accessKey
	}
	if secretKey != "" {
		out["AWS_SECRET_ACCESS_KEY"] = secretKey
	}
	if n != nil {
		if r := strings.TrimSpace(n.S3Region); r != "" {
			out["AWS_DEFAULT_REGION"] = r
		}
	}
	if ep := strings.TrimSpace(endpoint); ep != "" {
		out["AWS_ENDPOINT_URL"] = ep
		out["AWS_ENDPOINT_URL_S3"] = ep
	}
	if svcName == "minio" && n != nil {
		if mu := strings.TrimSpace(n.MinioRootUser); mu != "" {
			out["MINIO_ROOT_USER"] = mu
		}
		if mp := strings.TrimSpace(n.MinioRootPassword); mp != "" {
			out["MINIO_ROOT_PASSWORD"] = mp
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ResolvedVaultCLIEnv builds VAULT_ADDR / VAULT_TOKEN for the vault CLI wrapper.
// selection must be local or nomad (vault service does not use cred_source.vault).
func ResolvedVaultCLIEnv(ctx context.Context, cfg *config.Config, selection string) (map[string]string, error) {
	c := cfg.ActiveCtx()
	if !c.IsABCNodesCluster() {
		return nil, nil
	}
	svc := config.AdminFloorServiceNamed(&c.Admin.Services, "vault")
	if svc == nil {
		return nil, nil
	}

	addr, err := ResolveAdminFloorField(ctx, c, svc, "vault", selection, "http")
	if err != nil {
		return nil, err
	}
	addr = strings.TrimSpace(addr)
	addr = strings.TrimSuffix(addr, "/")
	if addr == "" {
		return nil, nil
	}
	out := map[string]string{"VAULT_ADDR": addr}

	tok, err := ResolveAdminFloorField(ctx, c, svc, "vault", selection, "access_key")
	if err != nil {
		return nil, err
	}
	if t := strings.TrimSpace(tok); t != "" {
		out["VAULT_TOKEN"] = t
	}
	return out, nil
}
