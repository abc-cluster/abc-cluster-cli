package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
	"github.com/spf13/cobra"
)

// initFrameworks is the set `abc app init` scaffolds. Mirrors the phase-1
// supported frameworks plus `custom`.
var initFrameworks = map[string]bool{
	"streamlit": true,
	"shiny":     true,
	"pode":      true,
	"static":    true,
	"custom":    true,
}

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold an abc-app.yaml in the current directory",
		Long: `Write a pre-filled abc-app.yaml into the current directory, ready to edit
and 'abc app deploy'. Port/health are pre-filled from the framework's defaults;
data/env/resources are commented-out starter blocks.

With --with-dockerfile, also writes a minimal framework-appropriate Dockerfile
honouring the bind contract (listen on 0.0.0.0 at the declared port).

Refuses to overwrite an existing abc-app.yaml (or Dockerfile) without --force.`,
		Args: cobra.NoArgs,
		RunE: runInit,
	}
	cmd.Flags().String("framework", "streamlit", "App framework: streamlit|shiny|pode|static|custom")
	cmd.Flags().String("name", "", "App name (default: a framework-flavoured placeholder)")
	cmd.Flags().String("project", "", "Project/group you deploy as")
	cmd.Flags().Bool("with-dockerfile", false, "Also write a minimal framework Dockerfile starter")
	cmd.Flags().Bool("force", false, "Overwrite existing files")
	return cmd
}

func runInit(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	fw := strings.ToLower(strings.TrimSpace(mustString(cmd, "framework")))
	if !initFrameworks[fw] {
		return fmt.Errorf("framework %q is not scaffoldable; use one of: streamlit, shiny, pode, static, custom", fw)
	}
	opts := appgen.ScaffoldOptions{
		Framework: fw,
		Name:      mustString(cmd, "name"),
		Project:   mustString(cmd, "project"),
	}
	force, _ := cmd.Flags().GetBool("force")
	withDockerfile, _ := cmd.Flags().GetBool("with-dockerfile")

	specPath := appgen.DefaultSpecFile
	if err := writeScaffold(specPath, appgen.ScaffoldYAML(opts), force); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %s (framework %s)\n", specPath, fw)

	if withDockerfile {
		dockerPath := "Dockerfile"
		if err := writeScaffold(dockerPath, appgen.ScaffoldDockerfile(opts), force); err != nil {
			return err
		}
		fmt.Fprintf(out, "Wrote %s\n", dockerPath)
	}

	fmt.Fprintln(out, "Next: edit the file, then `abc app validate` and `abc app deploy`.")
	return nil
}

// writeScaffold writes content to path, refusing to clobber an existing file
// unless force is set.
func writeScaffold(path, content string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists — re-run with --force to overwrite", filepath.Base(path))
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func mustString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}
