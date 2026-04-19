package secrets

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

const vaultSecretsMount = "secret"
const vaultSecretsPrefix = "abc"

func vaultSecretPath(ns, name string) string {
	return vaultSecretsPrefix + "/" + ns + "/" + name
}

// vaultBaseURL resolves the Vault address from flags, env, or config.
func vaultBaseURL(cmd *cobra.Command, cfg *config.Config) string {
	if v := flagStringOrEmpty(cmd, "vault-addr"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("VAULT_ADDR")); v != "" {
		return strings.TrimRight(v, "/")
	}
	ctx := cfg.ActiveCtx()
	if h, ok := config.GetAdminFloorField(&ctx.Admin.Services, "vault", "http"); ok && h != "" {
		return strings.TrimRight(h, "/")
	}
	return "http://127.0.0.1:8200"
}

// vaultToken resolves the Vault token from flags or env.
func vaultToken(cmd *cobra.Command) string {
	if v := flagStringOrEmpty(cmd, "vault-token"); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("VAULT_TOKEN"))
}

// vaultDo executes a simple Vault HTTP request and returns the response body.
func vaultDo(method, url, token string, body io.Reader) (int, []byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(method, url, body) //nolint:noctx
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Vault-Token", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

func runVaultSet(cmd *cobra.Command, cfg *config.Config, name, value string) error {
	base := vaultBaseURL(cmd, cfg)
	tok := vaultToken(cmd)
	if tok == "" {
		return fmt.Errorf("Vault token required: set VAULT_TOKEN or pass --vault-token")
	}
	ns := nomadSecretsNamespace(cmd, cfg)
	kvPath := vaultSecretPath(ns, name)
	apiURL := fmt.Sprintf("%s/v1/%s/data/%s", base, vaultSecretsMount, kvPath)

	payload := fmt.Sprintf(`{"data":{"value":%q}}`, value)
	code, body, err := vaultDo(http.MethodPost, apiURL, tok, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("vault request: %w", err)
	}
	if code == http.StatusForbidden {
		return fmt.Errorf("permission denied: admin Vault token required to write secrets")
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("vault API %d: %s", code, strings.TrimSpace(string(body)))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Secret %q stored at %s/%s (vault)\n", name, vaultSecretsMount, kvPath)
	return nil
}

func runVaultGet(cmd *cobra.Command, cfg *config.Config, name string) error {
	base := vaultBaseURL(cmd, cfg)
	tok := vaultToken(cmd)
	if tok == "" {
		return fmt.Errorf("Vault token required: set VAULT_TOKEN or pass --vault-token")
	}
	ns := nomadSecretsNamespace(cmd, cfg)
	kvPath := vaultSecretPath(ns, name)
	apiURL := fmt.Sprintf("%s/v1/%s/data/%s", base, vaultSecretsMount, kvPath)

	code, body, err := vaultDo(http.MethodGet, apiURL, tok, nil)
	if err != nil {
		return fmt.Errorf("vault request: %w", err)
	}
	if code == http.StatusForbidden {
		return fmt.Errorf(
			"permission denied: this secret is readable only by job allocations at runtime\n"+
				"  Use 'abc secrets ref %s --backend vault' to get the HCL template reference",
			name,
		)
	}
	if code == http.StatusNotFound {
		return fmt.Errorf("secret %q not found in vault at %s/%s", name, vaultSecretsMount, kvPath)
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("vault API %d: %s", code, strings.TrimSpace(string(body)))
	}

	var resp struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("parse vault response: %w", err)
	}
	val, ok := resp.Data.Data["value"]
	if !ok {
		return fmt.Errorf("secret %q found but missing 'value' key", name)
	}
	fmt.Fprint(cmd.OutOrStdout(), val)
	return nil
}

func runVaultList(cmd *cobra.Command, cfg *config.Config) error {
	base := vaultBaseURL(cmd, cfg)
	tok := vaultToken(cmd)
	if tok == "" {
		return fmt.Errorf("Vault token required: set VAULT_TOKEN or pass --vault-token")
	}
	ns := nomadSecretsNamespace(cmd, cfg)
	listPath := vaultSecretsPrefix + "/" + ns
	apiURL := fmt.Sprintf("%s/v1/%s/metadata/%s?list=true", base, vaultSecretsMount, listPath)

	code, body, err := vaultDo(http.MethodGet, apiURL, tok, nil)
	if err != nil {
		return fmt.Errorf("vault request: %w", err)
	}
	if code == http.StatusForbidden {
		return fmt.Errorf("permission denied: insufficient Vault token permissions to list secrets")
	}
	if code == http.StatusNotFound {
		fmt.Fprintf(cmd.OutOrStdout(), "No secrets under %s/%s/ (vault)\n", vaultSecretsMount, listPath)
		return nil
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("vault API %d: %s", code, strings.TrimSpace(string(body)))
	}

	var resp struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("parse vault response: %w", err)
	}
	if len(resp.Data.Keys) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No secrets under %s/%s/ (vault)\n", vaultSecretsMount, listPath)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "SECRETS (vault backend, namespace %q):\n", ns)
	for _, k := range resp.Data.Keys {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", strings.TrimSuffix(k, "/"))
	}
	return nil
}

func runVaultDelete(cmd *cobra.Command, cfg *config.Config, name string) error {
	base := vaultBaseURL(cmd, cfg)
	tok := vaultToken(cmd)
	if tok == "" {
		return fmt.Errorf("Vault token required: set VAULT_TOKEN or pass --vault-token")
	}
	ns := nomadSecretsNamespace(cmd, cfg)
	kvPath := vaultSecretPath(ns, name)
	apiURL := fmt.Sprintf("%s/v1/%s/metadata/%s", base, vaultSecretsMount, kvPath)

	code, body, err := vaultDo(http.MethodDelete, apiURL, tok, nil)
	if err != nil {
		return fmt.Errorf("vault request: %w", err)
	}
	if code == http.StatusForbidden {
		return fmt.Errorf("permission denied: admin Vault token required to delete secrets")
	}
	if code != http.StatusNoContent && (code < 200 || code >= 300) {
		return fmt.Errorf("vault API %d: %s", code, strings.TrimSpace(string(body)))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Secret %q deleted from vault.\n", name)
	return nil
}

// vaultSecretRef returns the Nomad template HCL snippet to read a Vault KV v2 secret at runtime.
func vaultSecretRef(ns, name string) string {
	kvPath := vaultSecretPath(ns, name)
	return fmt.Sprintf(`{{ with secret "%s/data/%s" }}{{ .Data.data.value }}{{ end }}`,
		vaultSecretsMount, kvPath)
}
