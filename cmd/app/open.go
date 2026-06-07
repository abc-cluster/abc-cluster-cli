package app

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

func newOpenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "open <name>",
		Short: "Open an app's URL in the default browser",
		Long: `Resolve a deployed app's URL (like 'abc app url') and open it in the
default browser. With --print, prints the URL instead of opening it — useful on
headless machines or for piping.`,
		Args: cobra.ExactArgs(1),
		RunE: runOpen,
	}
	cmd.Flags().Bool("print", false, "Print the URL instead of opening a browser")
	return cmd
}

func runOpen(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	nc := nomadClientFromCmd(cmd)
	r, err := resolveApp(cmd.Context(), nc, args[0])
	if err != nil {
		return err
	}
	url := appURLFromJob(r)
	if url == "" {
		return fmt.Errorf("could not resolve a URL for app %q", r.Name)
	}

	printOnly, _ := cmd.Flags().GetBool("print")
	if printOnly {
		fmt.Fprintln(out, url)
		return nil
	}

	if err := openBrowser(url); err != nil {
		// Fall back to printing so the URL is never lost when no opener exists.
		fmt.Fprintf(cmd.ErrOrStderr(), "  could not open a browser (%v); the URL is:\n", err)
		fmt.Fprintln(out, url)
		return nil
	}
	fmt.Fprintf(out, "Opening %s\n", url)
	return nil
}

// openBrowser opens url with the OS-native opener.
func openBrowser(url string) error {
	var bin string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		bin, args = "open", []string{url}
	case "windows":
		bin, args = "cmd", []string{"/c", "start", "", url}
	default: // linux and other unixes
		bin, args = "xdg-open", []string{url}
	}
	return exec.Command(bin, args...).Start()
}
