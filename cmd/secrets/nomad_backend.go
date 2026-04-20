package secrets

import (
	"context"
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

const nomadSecretsPrefix = "abc/secrets"

func nomadSecretPath(ns, name string) string {
	return nomadSecretsPrefix + "/" + ns + "/" + name
}

// nomadClientForSecrets builds a NomadClient from flags and config fallback.
func nomadClientForSecrets(cmd *cobra.Command, cfg *config.Config) *utils.NomadClient {
	addr := flagStringOrEmpty(cmd, "nomad-addr")
	token := flagStringOrEmpty(cmd, "nomad-token")
	region := flagStringOrEmpty(cmd, "region")

	if addr == "" || token == "" {
		cfgAddr, cfgToken, cfgRegion := utils.NomadDefaultsFromConfig()
		if addr == "" {
			addr = cfgAddr
		}
		if token == "" {
			token = cfgToken
		}
		if region == "" {
			region = cfgRegion
		}
	}
	return utils.NewNomadClient(addr, token, region)
}

func nomadSecretsNamespace(cmd *cobra.Command, cfg *config.Config) string {
	if ns := flagStringOrEmpty(cmd, "namespace"); ns != "" {
		return ns
	}
	return cfg.ActiveCtx().AbcNodesNomadNamespaceOrDefault()
}

func runNomadSet(cmd *cobra.Command, cfg *config.Config, name, value string) error {
	nc := nomadClientForSecrets(cmd, cfg)
	ns := nomadSecretsNamespace(cmd, cfg)
	path := nomadSecretPath(ns, name)
	if err := nc.PutVariable(context.Background(), path, ns, map[string]string{"value": value}); err != nil {
		if strings.Contains(err.Error(), "403") {
			return fmt.Errorf("permission denied: admin Nomad token required to write secrets")
		}
		return fmt.Errorf("write nomad variable %q: %w", path, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Secret %q stored at %s (namespace: %s)\n", name, path, ns)
	return nil
}

func runNomadGet(cmd *cobra.Command, cfg *config.Config, name string) error {
	nc := nomadClientForSecrets(cmd, cfg)
	ns := nomadSecretsNamespace(cmd, cfg)
	path := nomadSecretPath(ns, name)
	v, err := nc.GetVariable(context.Background(), path, ns)
	if err != nil {
		if strings.Contains(err.Error(), "403") {
			return fmt.Errorf(
				"permission denied: this secret is readable only by job allocations at runtime\n"+
					"  Use 'abc secrets ref %s --backend nomad' to get the HCL template reference",
				name,
			)
		}
		if strings.Contains(err.Error(), "404") {
			return fmt.Errorf("secret %q not found in namespace %q", name, ns)
		}
		return fmt.Errorf("get nomad variable %q: %w", path, err)
	}
	val, ok := v.Items["value"]
	if !ok {
		return fmt.Errorf("secret %q found but missing 'value' key", name)
	}
	fmt.Fprint(cmd.OutOrStdout(), val)
	return nil
}

func runNomadList(cmd *cobra.Command, cfg *config.Config) error {
	nc := nomadClientForSecrets(cmd, cfg)
	ns := nomadSecretsNamespace(cmd, cfg)
	listPrefix := nomadSecretsPrefix + "/" + ns + "/"
	stubs, err := nc.ListVariables(context.Background(), listPrefix, ns)
	if err != nil {
		if strings.Contains(err.Error(), "403") {
			return fmt.Errorf("permission denied: insufficient token permissions to list secrets")
		}
		return fmt.Errorf("list nomad variables: %w", err)
	}
	if len(stubs) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No secrets under %s (namespace: %s)\n", listPrefix, ns)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "SECRETS (nomad backend, namespace %q):\n", ns)
	for _, s := range stubs {
		name := strings.TrimPrefix(s.Path, listPrefix)
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", name)
	}
	return nil
}

func runNomadDelete(cmd *cobra.Command, cfg *config.Config, name string) error {
	nc := nomadClientForSecrets(cmd, cfg)
	ns := nomadSecretsNamespace(cmd, cfg)
	path := nomadSecretPath(ns, name)
	if err := nc.DeleteVariable(context.Background(), path, ns); err != nil {
		if strings.Contains(err.Error(), "403") {
			return fmt.Errorf("permission denied: admin Nomad token required to delete secrets")
		}
		return fmt.Errorf("delete nomad variable %q: %w", path, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Secret %q deleted.\n", name)
	return nil
}

func nomadSecretRef(ns, name string) string {
	path := nomadSecretPath(ns, name)
	return fmt.Sprintf("{{ with nomadVar %q }}{{ index . \"value\" }}{{ end }}", path)
}

func runNomadBackendSetup(cmd *cobra.Command, cfg *config.Config) error {
	nc := nomadClientForSecrets(cmd, cfg)
	ns := nomadSecretsNamespace(cmd, cfg)

	policyName := "abc-secrets-alloc-read"
	rules := fmt.Sprintf("variables {\n  path %q {\n    capabilities = [\"read\"]\n  }\n}\n",
		nomadSecretsPrefix+"/"+ns+"/*")

	body := map[string]interface{}{
		"Name":        policyName,
		"Description": "Allows Nomad job allocations to read abc secrets variables at runtime",
		"Rules":       rules,
	}
	if err := nc.ApplyACLPolicy(context.Background(), policyName, body); err != nil {
		return fmt.Errorf("create ACL policy %q: %w", policyName, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "ACL policy %q created/updated.\n", policyName)
	fmt.Fprintf(cmd.OutOrStdout(), "Job allocations can now read secrets under %s/%s/*\n", nomadSecretsPrefix, ns)
	return nil
}

func flagStringOrEmpty(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(v)
}
