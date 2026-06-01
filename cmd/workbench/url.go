package workbench

// url.go — `abc workbench url` is DEPRECATED.
//
// The workbench has two access paths, each owned by a clearer command:
//
//   Browser → abc portal open workbench   (magic-link, drops you in logged-in)
//   Editor  → abc workbench connect        (token, for VS Code / Positron)
//
// `abc workbench url` printed a bare, manual-login URL — a worse copy of
// `abc portal open workbench`. It is kept only as a deprecated shim that
// redirects to the two paths above (and still prints the bare browser URL for
// headless / SSH use where you cannot open a browser).

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newURLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:        "url",
		Short:      "(deprecated) Print the workbench browser URL",
		Deprecated: "use `abc portal open workbench` (browser) or `abc workbench connect` (VS Code / Positron).",
		Hidden:     true,
		RunE:       runURL,
	}
	return cmd
}

func runURL(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "The workbench has two access paths:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Browser (logged in automatically):")
	fmt.Fprintln(out, "    abc portal open workbench")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Editor — VS Code / Positron (kernel connection):")
	fmt.Fprintln(out, "    abc workbench connect --client positron")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Browser URL (manual login): %s\n", hubURL(ctx))
	return nil
}
