package samplesheet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	hclgenmodule "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/module"
	"github.com/spf13/cobra"
)

// Defaults that match `module run`. Kept loose — the emit job is small
// and short-lived, so fancy resource tuning isn't worth the surface area.
const (
	defaultNfVersion          = "25.10.4"
	defaultPipelineGenRepo    = "abc-cluster/nf-pipeline-gen"
	defaultPipelineGenVersion = "latest"
	defaultDatacenter         = "*"
	defaultTaskDriver         = "docker"
	defaultWaitTimeout        = 3 * time.Minute
)

// TODO(future): The intended UX for samplesheet scaffolding is:
//
//	abc module run nf-core/fastqc --emit-samplesheet
//
// That would combine samplesheet generation and module execution into a single
// command, removing the need to run "abc module samplesheet emit" as a
// separate step. The current "emit" subcommand is a stepping stone; it should
// be replaced with an --emit-samplesheet flag on "abc module run" once the
// nf-pipeline-gen integration supports it end-to-end.
func newEmitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "emit <module>",
		Short: "Scaffold a starter samplesheet from a module's bundled tests",
		Long: `Submit a Nomad batch job that runs nf-pipeline-gen against the
module's bundled tests/main.nf.test fixtures, captures the resulting CSV
into a Nomad Variable, then downloads it to a local file.

Pair with 'abc module run --samplesheet PATH' once you've edited the CSV
to point at your real data.

Example:
  abc module samplesheet emit nf-core/plink/extract
  # → ./samplesheet-nf-core-plink-extract.csv`,
		Args: cobra.ExactArgs(1),
		RunE: runEmit,
	}

	cmd.Flags().String("output", "", "Local CSV output path (default: ./samplesheet-<module-slug>.csv)")
	cmd.Flags().String("driver", "", "Nomad task driver for the emit job (default: docker)")
	cmd.Flags().StringSlice("datacenter", nil, "Nomad datacenter(s) (default: dc1)")
	cmd.Flags().String("nf-version", "", "Nextflow Docker image tag for the emit task (default: 25.10.4)")
	cmd.Flags().String("pipeline-gen-repo", defaultPipelineGenRepo, "GitHub repo for nf-pipeline-gen release assets")
	cmd.Flags().String("pipeline-gen-version", defaultPipelineGenVersion, "nf-pipeline-gen release tag (or 'latest')")
	cmd.Flags().String("pipeline-gen-url-base", "", "Direct URL base for the JAR (mirror); skips GitHub when set")
	cmd.Flags().String("github-token", utils.EnvOrDefault("GITHUB_TOKEN", "GH_TOKEN"), "GitHub token for release download (or set GITHUB_TOKEN/GH_TOKEN)")
	cmd.Flags().Duration("wait-timeout", defaultWaitTimeout, "Maximum time to wait for the emit job to complete")
	cmd.Flags().Bool("dry-run", false, "Print generated HCL without submitting")

	return cmd
}

func runEmit(cmd *cobra.Command, args []string) error {
	moduleName := args[0]

	addr, token, region, namespace := resolveNomadFlags(cmd)
	nc := utils.NewNomadClient(addr, token, region).
		WithSudo(utils.SudoFromCmd(cmd)).
		WithCloud(utils.CloudFromCmd(cmd))

	spec := hclgenmodule.EmitSpec{
		JobName:    "ss-emit-" + moduleSlug(moduleName) + "-" + shortRunID(),
		Module:     moduleName,
		TaskDriver: defaultTaskDriver,
		PipelineGenRepo:    defaultPipelineGenRepo,
		PipelineGenVersion: defaultPipelineGenVersion,
		NfVersion:          defaultNfVersion,
		Datacenters:        []string{defaultDatacenter},
		Namespace:          namespace,
	}
	if v, _ := cmd.Flags().GetString("driver"); v != "" {
		spec.TaskDriver = v
	}
	if v, _ := cmd.Flags().GetStringSlice("datacenter"); len(v) > 0 {
		spec.Datacenters = v
	}
	if v, _ := cmd.Flags().GetString("nf-version"); v != "" {
		spec.NfVersion = v
	}
	if v, _ := cmd.Flags().GetString("pipeline-gen-repo"); v != "" {
		spec.PipelineGenRepo = v
	}
	if v, _ := cmd.Flags().GetString("pipeline-gen-version"); v != "" {
		spec.PipelineGenVersion = v
	}
	if v, _ := cmd.Flags().GetString("pipeline-gen-url-base"); v != "" {
		spec.PipelineGenURLBase = v
	}
	if v, _ := cmd.Flags().GetString("github-token"); v != "" {
		spec.GitHubToken = v
	}

	if spec.GitHubToken == "" && spec.PipelineGenURLBase == "" {
		return fmt.Errorf("missing GitHub token: set GITHUB_TOKEN or GH_TOKEN env var, or pass --pipeline-gen-url-base")
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "" {
		output = "./samplesheet-" + moduleSlug(moduleName) + ".csv"
	}

	runUUID := newRunUUID()
	hcl := hclgenmodule.GenerateEmit(spec, addr, token, runUUID)

	if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
		fmt.Fprint(cmd.OutOrStdout(), hcl)
		return nil
	}

	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	fmt.Fprintf(stderr, "  Submitting samplesheet emit job %s ...\n", spec.JobName)
	jobJSON, err := nc.ParseHCL(ctx, hcl)
	if err != nil {
		return fmt.Errorf("nomad HCL parse: %w", err)
	}
	if _, err := nc.RegisterJob(ctx, jobJSON); err != nil {
		return fmt.Errorf("nomad register: %w", err)
	}
	fmt.Fprintf(out, "  Job        %s\n", spec.JobName)

	timeout, _ := cmd.Flags().GetDuration("wait-timeout")
	terminalErr := waitForTerminal(ctx, nc, spec.JobName, namespace, timeout, stderr)

	// The emit task ALWAYS publishes its variable at end-of-script,
	// success or fail (on fail it stashes the tail of /local/emit.log
	// under "diag" so the CLI can show it even without alloc-log perms).
	// So we read the variable regardless of terminalErr — that gives us a
	// useful error message in either branch.
	v, varErr := nc.GetVariable(ctx, hclgenmodule.VariablePathForEmit(spec.JobName), namespace)
	if terminalErr != nil {
		if varErr == nil && v != nil {
			if diag := v.Items["diag"]; diag != "" {
				fmt.Fprintf(stderr, "\n--- emit task log (last 16 KB) ---\n%s\n--- end ---\n", diag)
			}
		}
		return terminalErr
	}
	if varErr != nil {
		return fmt.Errorf("read samplesheet variable: %w (job state was terminal but variable not published — check `abc job show %s --namespace %s`)", varErr, spec.JobName, namespace)
	}
	csvText, ok := v.Items[hclgenmodule.VariableKeyForEmit]
	if !ok || csvText == "" {
		if diag := v.Items["diag"]; diag != "" {
			fmt.Fprintf(stderr, "\n--- emit task log (last 16 KB) ---\n%s\n--- end ---\n", diag)
		}
		return fmt.Errorf("samplesheet variable is empty (job ended without producing CSV)")
	}

	if err := os.WriteFile(output, []byte(csvText), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", output, err)
	}
	fmt.Fprintf(out, "  Wrote %s\n", output)
	previewHeader(out, csvText)
	fmt.Fprintf(out, "\nNext: edit %s, then run\n", output)
	fmt.Fprintf(out, "  abc module run %s --samplesheet %s\n", moduleName, output)
	return nil
}

// waitForTerminal polls the job's allocations until they reach a terminal
// client status (`complete`, `failed`, `lost`). Returns an error on
// non-success terminal states or on timeout.
func waitForTerminal(ctx context.Context, nc *utils.NomadClient, jobID, namespace string,
	timeout time.Duration, w io.Writer) error {
	start := time.Now()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) > timeout {
			return fmt.Errorf("timed out after %s waiting for emit job to finish; check `abc job show %s`", timeout, jobID)
		}
		allocs, err := nc.GetJobAllocs(ctx, jobID, namespace, false)
		if err != nil {
			return fmt.Errorf("get job allocs: %w", err)
		}
		if len(allocs) == 0 {
			fmt.Fprintf(w, "  Waiting for allocation...\n")
			time.Sleep(2 * time.Second)
			continue
		}
		alloc := allocs[len(allocs)-1]
		if !utils.AllocClientTerminalStatus(alloc.ClientStatus) {
			fmt.Fprintf(w, "  Status: %s ...\n", alloc.ClientStatus)
			time.Sleep(3 * time.Second)
			continue
		}
		if alloc.ClientStatus != "complete" {
			return fmt.Errorf("emit job %s ended with status %q; run `abc job logs %s` for details", jobID, alloc.ClientStatus, jobID)
		}
		return nil
	}
}

func previewHeader(w io.Writer, csv string) {
	lines := strings.SplitN(csv, "\n", 3)
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		fmt.Fprintf(w, "    %s\n", ln)
	}
}

func resolveNomadFlags(cmd *cobra.Command) (addr, token, region, namespace string) {
	addr, _ = cmd.Flags().GetString("nomad-addr")
	if addr == "" {
		addr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	token, _ = cmd.Flags().GetString("nomad-token")
	if token == "" {
		token, _ = cmd.Root().PersistentFlags().GetString("nomad-token")
	}
	region, _ = cmd.Flags().GetString("region")
	if region == "" {
		region, _ = cmd.Root().PersistentFlags().GetString("region")
	}
	if addr == "" || token == "" || region == "" {
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
	namespace, _ = cmd.Flags().GetString("namespace")
	if namespace == "" {
		namespace, _ = cmd.Root().PersistentFlags().GetString("namespace")
	}
	if namespace == "" {
		// Mirror what other commands like `abc job list` already do under
		// abc-bootstrap: fall back to the active context's
		// admin.abc_nodes.nomad_namespace (the place real cluster jobs run).
		// Without this the emit job lands in `default`, which on abc-nodes
		// clusters has zero nodes and the job sits queued forever.
		if cfg, err := config.Load(); err == nil && cfg != nil {
			namespace = cfg.ActiveCtx().AbcNodesNomadNamespaceForCLI()
		}
	}
	return
}

func moduleSlug(name string) string {
	s := strings.ToLower(name)
	r := strings.NewReplacer("/", "-", ".", "-", "_", "-", " ", "-")
	return strings.Trim(r.Replace(s), "-")
}

func newRunUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("run-%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}

func shortRunID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
