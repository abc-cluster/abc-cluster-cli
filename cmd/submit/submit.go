package submit

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	jobpkg "github.com/abc-cluster/abc-cluster-cli/cmd/job"
	modulepkg "github.com/abc-cluster/abc-cluster-cli/cmd/module"
	pipelinepkg "github.com/abc-cluster/abc-cluster-cli/cmd/pipeline"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

const (
	watchDelay   = 10 * time.Second
	watchTimeout = 5 * time.Minute
)

func runSubmit(cmd *cobra.Command, args []string) error {
	target := args[0]
	ctx := cmd.Context()

	// Connection details.
	nomadAddr := nomadAddrFromCmd(cmd)
	nomadToken := nomadTokenFromCmd(cmd)
	namespace := namespaceFromCmd(cmd)

	nc := utils.NewNomadClient(nomadAddr, nomadToken, "").
		WithSudo(utils.SudoFromCmd(cmd)).
		WithCloud(utils.CloudFromCmd(cmd))

	// Collect detection inputs.
	forceType, _ := cmd.Flags().GetString("type")

	tt, err := detectTargetType(ctx, nc, target, forceType, namespace)
	if err != nil {
		return err
	}
	if tt == typeUnknown {
		return fmt.Errorf("cannot determine type for %q; use --type pipeline|job|module", target)
	}

	// Build params file from --input / --output / --param flags.
	input, _ := cmd.Flags().GetString("input")
	output, _ := cmd.Flags().GetString("output")
	extraParams, _ := cmd.Flags().GetStringArray("param")
	paramsPath, cleanParams, err := buildParamsFile(input, output, extraParams)
	if err != nil {
		return err
	}
	defer cleanParams()

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	wait, _ := cmd.Flags().GetBool("wait")
	streamLogs, _ := cmd.Flags().GetBool("logs")
	name, _ := cmd.Flags().GetString("name")
	datacenters, _ := cmd.Flags().GetStringSlice("datacenter")

	var hcl, jobName string

	switch tt {
	case typePipeline:
		hcl, jobName, err = buildPipelineHCL(ctx, cmd, nc, target, paramsPath,
			name, namespace, nomadAddr, nomadToken, datacenters)
	case typeModule:
		hcl, jobName, err = buildModuleHCL(cmd, target, paramsPath,
			name, namespace, nomadAddr, nomadToken, datacenters)
	case typeJob:
		hcl, jobName, err = buildJobHCL(cmd, target, paramsPath,
			name, namespace, datacenters)
	}
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Fprint(cmd.OutOrStdout(), hcl)
		return nil
	}

	return submitAndWatch(ctx, cmd, nc, hcl, jobName, namespace, wait, streamLogs)
}

// buildPipelineHCL loads (or constructs) a pipeline spec and generates HCL.
func buildPipelineHCL(ctx context.Context, cmd *cobra.Command, nc *utils.NomadClient,
	target, paramsPath, name, namespace, nomadAddr, nomadToken string, datacenters []string) (string, string, error) {

	// Only attempt a saved-pipeline lookup when the target looks like a plain
	// name (no "/" and no URL scheme). Repo paths like "owner/repo" and full
	// URLs are always treated as ad-hoc Nextflow references.
	var saved *pipelinepkg.PipelineSpec
	if !strings.Contains(target, "/") && !strings.HasPrefix(target, "http") {
		var err error
		saved, err = pipelinepkg.LoadPipeline(ctx, nc, target, namespace)
		if err != nil {
			lower := strings.ToLower(err.Error())
			if strings.Contains(lower, "403") || strings.Contains(lower, "permission denied") {
				saved = nil // treat as ad-hoc
			} else {
				return "", "", err
			}
		}
	}
	base := saved
	if base == nil {
		base = &pipelinepkg.PipelineSpec{Repository: target}
	}

	override := &pipelinepkg.PipelineSpec{}
	if name != "" {
		override.Name = name
	}
	if namespace != "" {
		override.Namespace = namespace
	}
	if len(datacenters) > 0 {
		override.Datacenters = datacenters
	}
	if v, _ := cmd.Flags().GetString("revision"); v != "" {
		override.Revision = v
	}
	if v, _ := cmd.Flags().GetString("profile"); v != "" {
		override.Profile = v
	}
	if v, _ := cmd.Flags().GetString("config"); v != "" {
		data, err := readFile(v)
		if err != nil {
			return "", "", fmt.Errorf("reading --config %q: %w", v, err)
		}
		override.ExtraConfig = string(data)
	}
	if v, _ := cmd.Flags().GetString("work-dir"); v != "" {
		override.WorkDir = v
	}
	if v, _ := cmd.Flags().GetString("nf-version"); v != "" {
		override.NfVersion = v
	}
	if paramsPath != "" {
		params, err := utils.LoadParamsFile(paramsPath)
		if err != nil {
			return "", "", fmt.Errorf("reading params file: %w", err)
		}
		override.Params = params
	}

	spec := pipelinepkg.MergeSpec(base, override)
	spec.Defaults()

	uuid := pipelinepkg.NewPipelineRunUUID()
	hcl := pipelinepkg.GenerateHeadJobHCL(spec, nomadAddr, nomadToken, uuid)

	jobName := "nextflow-head"
	if spec.Name != "" {
		jobName = spec.Name
	}
	return hcl, jobName, nil
}

// buildModuleHCL constructs a RunSpec and generates HCL for a module run.
func buildModuleHCL(cmd *cobra.Command, target, paramsPath, name, namespace, nomadAddr, nomadToken string, datacenters []string) (string, string, error) {
	spec := &modulepkg.RunSpec{Module: target}
	if name != "" {
		spec.JobName = name
	}
	if namespace != "" {
		spec.Namespace = namespace
	}
	if len(datacenters) > 0 {
		spec.Datacenters = datacenters
	}
	if v, _ := cmd.Flags().GetString("profile"); v != "" {
		spec.Profile = v
	}
	if v, _ := cmd.Flags().GetString("work-dir"); v != "" {
		spec.WorkDir = v
	}
	if v, _ := cmd.Flags().GetString("nf-version"); v != "" {
		spec.NfVersion = v
	}
	if paramsPath != "" {
		data, err := readFile(paramsPath)
		if err != nil {
			return "", "", fmt.Errorf("reading params file: %w", err)
		}
		spec.ParamsYAMLContent = string(data)
	}

	hcl := modulepkg.BuildModuleHCL(spec, nomadAddr, nomadToken)
	return hcl, spec.JobName, nil
}

// buildJobHCL handles direct script submission. Conda/pixi wrappers are
// declared via #ABC preamble directives inside the script itself.
func buildJobHCL(cmd *cobra.Command, target, paramsPath, name, namespace string, datacenters []string) (string, string, error) {
	cores, _ := cmd.Flags().GetInt("cores")
	mem, _ := cmd.Flags().GetString("mem")
	memMB, _ := parseMemoryMBStr(mem)

	opts := jobpkg.ScriptHCLOptions{
		Name:      name,
		Namespace: namespace,
		Cores:     cores,
		MemoryMB:  memMB,
	}

	// paramsPath carries --input/--output/--param values; for script jobs these
	// are not yet injected into the HCL env block — the caller passes them via
	// the params file if the script preamble supports it.
	_ = paramsPath

	res, err := jobpkg.BuildScriptHCL(target, opts)
	if err != nil {
		return "", "", err
	}
	return res.HCL, res.JobName, nil
}

func submitAndWatch(ctx context.Context, cmd *cobra.Command, nc *utils.NomadClient,
	hcl, jobName, namespace string, wait, streamLogs bool) error {

	fmt.Fprintf(cmd.ErrOrStderr(), "  Parsing HCL via Nomad...\n")
	jobJSON, err := nc.ParseHCL(ctx, hcl)
	if err != nil {
		return fmt.Errorf("nomad HCL parse: %w", err)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "  Submitting job to Nomad...\n")
	resp, err := nc.RegisterJob(ctx, jobJSON)
	if err != nil {
		return fmt.Errorf("nomad register: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  Job submitted\n")
	fmt.Fprintf(out, "  Job        %s\n", jobName)
	fmt.Fprintf(out, "  Eval ID    %s\n", resp.EvalID)
	if resp.Warnings != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Warnings: %s\n", resp.Warnings)
	}

	if wait || streamLogs {
		fmt.Fprintln(cmd.ErrOrStderr(), "\n  Waiting for allocation...")
		var w io.Writer = io.Discard
		if streamLogs {
			w = out
		}
		return utils.WatchJobLogs(ctx, nc, jobName, namespace, w, watchDelay, watchTimeout)
	}

	fmt.Fprintf(out, "\n  Track progress:\n")
	fmt.Fprintf(out, "    abc job logs %s --follow\n", jobName)
	fmt.Fprintf(out, "    abc job show %s\n", jobName)
	return nil
}
