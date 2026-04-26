package terraform

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cli [terraform-args...]",
		Short: "Run the local Terraform CLI with abc context credentials pre-loaded",
		Long: `Run the local terraform binary as a passthrough alias.

Nomad credentials from the active abc config context are injected as
TF_VAR_nomad_address, TF_VAR_nomad_token, and TF_VAR_nomad_region so that
terraform plan / apply picks them up without a tfvars file.

Override credentials via persistent flags on the parent command:
  --nomad-addr, --nomad-token, --nomad-region

Use -- to pass argv verbatim to terraform:
  abc admin services terraform cli -- plan
  abc admin services terraform cli -- apply -auto-approve
  abc admin services terraform cli -- output -json

Optional leading --binary-location <path> before --:
  abc admin services terraform cli --binary-location /usr/local/bin/terraform -- plan`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runTerraformCLI,
	}
	return cmd
}

func runTerraformCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	binaryLocation = resolveTerraformBinary(binaryLocation)

	addr, token, region := terraformConnectionFromCmd(cmd)
	envOverrides := buildTFVarEnv(addr, token, region)

	return utils.RunExternalCLIWithEnv(
		cmd.Context(),
		passthroughArgs,
		binaryLocation,
		[]string{"terraform"},
		envOverrides,
		os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr(),
	)
}

// resolveTerraformBinary returns binaryLocation if set, otherwise searches
// ABC_TERRAFORM_CLI_BINARY / TERRAFORM_CLI_BINARY / TERRAFORM_BINARY env vars,
// then falls back to the managed binary at ~/.abc/binaries/terraform.
func resolveTerraformBinary(binaryLocation string) string {
	if binaryLocation != "" {
		return binaryLocation
	}
	if loc := utils.EnvOrDefault(
		"ABC_TERRAFORM_CLI_BINARY",
		"TERRAFORM_CLI_BINARY",
		"TERRAFORM_BINARY",
	); loc != "" {
		return loc
	}
	if managedPath, err := utils.ManagedBinaryPath("terraform"); err == nil {
		if info, err := os.Stat(managedPath); err == nil && !info.IsDir() {
			return managedPath
		}
	}
	return "" // RunExternalCLI will search PATH
}

// terraformConnectionFromCmd resolves Nomad credentials for TF_VAR_* injection.
// Priority: persistent flags set on the parent cmd → config file → empty.
func terraformConnectionFromCmd(cmd *cobra.Command) (addr, token, region string) {
	// Start from config defaults.
	addr, token, region = terraformDefaultsFromConfig()

	// Walk up to find the parent "terraform" command where the persistent
	// flags are defined, since DisableFlagParsing prevents accessing them
	// directly on this cmd.
	parentCmd := cmd.Parent()
	if parentCmd != nil {
		if f := parentCmd.PersistentFlags().Lookup("nomad-addr"); f != nil && f.Changed {
			addr = f.Value.String()
		} else if addr == "" {
			addr = utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR")
		}

		if f := parentCmd.PersistentFlags().Lookup("nomad-token"); f != nil && f.Changed {
			token = f.Value.String()
		} else if token == "" {
			token = utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN")
		}

		if f := parentCmd.PersistentFlags().Lookup("nomad-region"); f != nil && f.Changed {
			region = f.Value.String()
		} else if region == "" {
			region = utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION")
		}
	}
	return addr, token, region
}

func terraformDefaultsFromConfig() (addr, token, region string) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return "", "", ""
	}
	active := cfg.ActiveCtx()
	return active.NomadAddr(), active.NomadToken(), active.NomadRegion()
}

// terraformVarsFromConfig returns the extra TF_VAR_* map from
// admin.services.terraform.vars in the active config context.
func terraformVarsFromConfig() map[string]string {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return map[string]string{}
	}
	return cfg.ActiveCtx().TerraformVars()
}

// buildTFVarEnv maps resolved Nomad credentials to TF_VAR_* keys so Terraform
// picks them up automatically for the matching variable declarations.
// Extra vars from admin.services.terraform.vars are merged in last (lower
// priority than credentials so they cannot accidentally wipe a token).
func buildTFVarEnv(addr, token, region string) map[string]string {
	env := map[string]string{}

	// Merge config-level extra vars first (lowest priority).
	for k, v := range terraformVarsFromConfig() {
		if k != "" && v != "" {
			env["TF_VAR_"+k] = v
		}
	}

	// Nomad credentials override any conflicting config vars.
	if addr != "" {
		env["TF_VAR_nomad_address"] = addr
		// Also export NOMAD_ADDR so the Nomad provider can discover the server
		// even when the tfvars variable is named differently.
		env["NOMAD_ADDR"] = addr
	}
	if token != "" {
		env["TF_VAR_nomad_token"] = token
		env["NOMAD_TOKEN"] = token
	}
	if region != "" {
		env["TF_VAR_nomad_region"] = region
		env["NOMAD_REGION"] = region
	}
	return env
}
