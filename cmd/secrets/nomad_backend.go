package secrets

import (
	"context"
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/secretref"
	"github.com/spf13/cobra"
)

const nomadSecretsPrefix = "abc/secrets"

func nomadSecretPath(ns, name string) string {
	return secretref.NomadPath(ns, name)
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
		// Nomad filters list results by ACL without an error, so an empty
		// result can also mean "exists, but this token may not read it".
		fmt.Fprintf(cmd.OutOrStdout(), "No secrets visible under %s (namespace: %s)\n", listPrefix, ns)
		fmt.Fprintf(cmd.OutOrStdout(), "  Note: Nomad hides variables your token cannot read; secrets set with an admin token may still exist.\n")
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
	return secretref.TemplateRef(secretref.BackendNomad, ns, name)
}

// allocReadPolicyName is the per-namespace policy that lets job allocations
// read abc secrets. One policy per namespace: a single shared name would make
// each `setup --namespace X` overwrite the previous namespace's grant.
func allocReadPolicyName(ns string) string {
	return "abc-secrets-alloc-read-" + ns
}

// allocReadPolicyRules grants read on abc/secrets/<ns>/*. Nomad only accepts
// a variables rule nested in a namespace block; a top-level `variables` block
// fails strict policy parsing.
func allocReadPolicyRules(ns string) string {
	return fmt.Sprintf("namespace %q {\n  variables {\n    path %q {\n      capabilities = [\"read\"]\n    }\n  }\n}\n",
		ns, secretref.NomadPolicyPathGlob(ns))
}

// allocReadPolicyBody is the PUT /v1/acl/policy body. JobACL with only a
// Namespace attaches the policy to the workload identity of every job in that
// namespace; without JobACL it is a plain token policy that allocations never
// receive, so their nomadVar templates block as "Missing".
func allocReadPolicyBody(ns string) map[string]interface{} {
	return map[string]interface{}{
		"Name":        allocReadPolicyName(ns),
		"Description": "Allows job allocations in namespace " + ns + " to read abc secrets at runtime",
		"Rules":       allocReadPolicyRules(ns),
		"JobACL":      map[string]string{"Namespace": ns},
	}
}

func runNomadBackendSetup(cmd *cobra.Command, cfg *config.Config) error {
	nc := nomadClientForSecrets(cmd, cfg)
	ns := nomadSecretsNamespace(cmd, cfg)

	body := allocReadPolicyBody(ns)
	name := allocReadPolicyName(ns)
	if err := nc.ApplyACLPolicy(context.Background(), name, body); err != nil {
		if strings.Contains(err.Error(), "403") {
			return fmt.Errorf("permission denied: creating ACL policy %q needs a Nomad token with acl:write (management)", name)
		}
		return fmt.Errorf("create ACL policy %q: %w", name, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "ACL policy %q created/updated (attached to all jobs in namespace %s).\n", name, ns)
	fmt.Fprintf(cmd.OutOrStdout(), "Job allocations in %s can now read secrets under %s\n", ns, secretref.NomadPolicyPathGlob(ns))
	return nil
}

func flagStringOrEmpty(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(v)
}
