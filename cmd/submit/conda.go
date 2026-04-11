package submit

import (
	"fmt"
	"os"
	"strings"
)

// generateCondaWrapper creates a temporary bash script that wraps a conda
// tool invocation. solver must be one of: conda, mamba, micromamba.
//
// Returns the temp file path and a cleanup function (removes the file).
func generateCondaWrapper(tool, condaSpec, solver string, cores, memoryMB int, walltime string, toolArgs []string) (path string, cleanup func(), err error) {
	if solver == "" {
		solver = "conda"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "#!/bin/bash\n# Generated %s wrapper for: %s\n", solver, tool)
	// Emit ABC metadata so the job carries introspectable labels.
	fmt.Fprintf(&sb, "#ABC --conda=%s\n", condaSpec)
	if cores > 0 {
		fmt.Fprintf(&sb, "#ABC --cores=%d\n", cores)
	}
	if memoryMB > 0 {
		fmt.Fprintf(&sb, "#ABC --mem=%dM\n", memoryMB)
	}
	if walltime != "" {
		fmt.Fprintf(&sb, "#ABC --time=%s\n", walltime)
	}
	sb.WriteString("\n")
	// Call the solver directly so the script works regardless of node-side
	// conda integration.
	fmt.Fprintf(&sb, "%s run --no-capture-output -n %s", solver, condaSpec)
	fmt.Fprintf(&sb, " %s", tool)
	for _, arg := range toolArgs {
		fmt.Fprintf(&sb, " %s", arg)
	}
	sb.WriteString(" \"${INPUT:-}\"\n")

	return writeTempScript("abc-submit-conda-*.sh", sb.String())
}

// generatePixiWrapper creates a temporary bash script that runs a tool via pixi.
//
// Returns the temp file path and a cleanup function (removes the file).
func generatePixiWrapper(tool string, cores, memoryMB int, walltime string, toolArgs []string) (path string, cleanup func(), err error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "#!/bin/bash\n# Generated pixi wrapper for: %s\n", tool)
	if cores > 0 {
		fmt.Fprintf(&sb, "#ABC --cores=%d\n", cores)
	}
	if memoryMB > 0 {
		fmt.Fprintf(&sb, "#ABC --mem=%dM\n", memoryMB)
	}
	if walltime != "" {
		fmt.Fprintf(&sb, "#ABC --time=%s\n", walltime)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "pixi run %s", tool)
	for _, arg := range toolArgs {
		fmt.Fprintf(&sb, " %s", arg)
	}
	sb.WriteString(" \"${INPUT:-}\"\n")

	return writeTempScript("abc-submit-pixi-*.sh", sb.String())
}

// writeTempScript writes content to a new temp file with the given pattern,
// chmod 0755, and returns the path + cleanup function.
func writeTempScript(pattern, content string) (path string, cleanup func(), err error) {
	noop := func() {}

	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", noop, fmt.Errorf("creating temp script: %w", err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", noop, fmt.Errorf("writing temp script: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", noop, fmt.Errorf("closing temp script: %w", err)
	}
	if err := os.Chmod(f.Name(), 0755); err != nil {
		os.Remove(f.Name())
		return "", noop, fmt.Errorf("chmod temp script: %w", err)
	}
	path = f.Name()
	cleanup = func() { os.Remove(path) }
	return path, cleanup, nil

}
