package job

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newDispatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dispatch <job-id>",
		Short: "Dispatch an instance of a parameterized Nomad batch job",
		Args:  cobra.ExactArgs(1),
		RunE:  runDispatch,
	}
	cmd.Flags().StringArray("meta", nil,
		"Meta key=value pair to pass to the dispatched job (repeatable)")
	cmd.Flags().String("input", "",
		"Path to a file whose contents are passed as the dispatch payload")
	cmd.Flags().Bool("detach", false, "Do not wait for the dispatched allocation to start")
	return cmd
}

func runDispatch(cmd *cobra.Command, args []string) error {
	jobID := args[0]
	metaSlice, _ := cmd.Flags().GetStringArray("meta")
	inputFile, _ := cmd.Flags().GetString("input")

	meta := make(map[string]string, len(metaSlice))
	for _, kv := range metaSlice {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("--meta must be key=value, got %q", kv)
		}
		meta[parts[0]] = parts[1]
	}

	var payload []byte
	if inputFile != "" {
		var err error
		payload, err = os.ReadFile(inputFile)
		if err != nil {
			return fmt.Errorf("reading --input file %q: %w", inputFile, err)
		}
	}

	nc := nomadClientFromCmd(cmd)
	resp, err := nc.DispatchJob(cmd.Context(), jobID, meta, payload)
	if err != nil {
		return fmt.Errorf("dispatching job %q: %w", jobID, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  ✓ Dispatched\n")
	fmt.Fprintf(out, "  Nomad job ID   %s\n", resp.DispatchedJobID)
	fmt.Fprintf(out, "  Evaluation ID  %s\n", resp.EvalID)
	return nil
}
