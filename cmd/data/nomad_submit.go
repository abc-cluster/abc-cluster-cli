package data

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

var nomadNodeUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func ociNomadDriver(driver string) bool {
	d := strings.ToLower(strings.TrimSpace(driver))
	return d == "docker" || d == "containerd" || d == "containerd-driver"
}

func placementConstraintPreamble(node string) string {
	node = strings.TrimSpace(node)
	if node == "" {
		return ""
	}
	if nomadNodeUUIDPattern.MatchString(node) {
		return fmt.Sprintf("#ABC --constraint=node.unique.id==%s", node)
	}
	return fmt.Sprintf("#ABC --constraint=node.unique.name==%s", node)
}

// downloadArtifact describes a single Nomad artifact stanza entry produced by
// the data download path.  Dest and Mode are optional; when empty Nomad uses
// its defaults ("local/" and "any" respectively).
type downloadArtifact struct {
	URL  string
	Dest string // e.g. "local/s5cmd"
	Mode string // e.g. "file"
}

// buildDataDownloadJobRunArgs builds argv for `abc job run --submit` for data-* Nomad wrappers.
func buildDataDownloadJobRunArgs(driver, image, jobName string, artifacts []downloadArtifact, scriptPath string) []string {
	nomadDriver := utils.NormalizeNomadTaskDriver(driver)
	args := []string{"job", "run", "--submit"}
	if n := strings.TrimSpace(jobName); n != "" {
		args = append(args, "--name", n)
	}
	for _, art := range artifacts {
		if strings.TrimSpace(art.URL) == "" {
			continue
		}
		// Encode dest/mode inline as "url|dest|mode" so each artifact carries its
		// own path — abc job run parses this format in parseArtifactFlagValue.
		encoded := art.URL
		if art.Dest != "" || art.Mode != "" {
			encoded = art.URL + "|" + art.Dest + "|" + art.Mode
		}
		args = append(args, "--artifact", encoded)
	}
	if ociNomadDriver(nomadDriver) {
		d := strings.ToLower(strings.TrimSpace(nomadDriver))
		args = append(args, "--driver", d, "--driver.config", fmt.Sprintf("image=%s", image), scriptPath)
		return args
	}
	args = append(args, "--driver", nomadDriver, scriptPath)
	return args
}

// dataNomadScriptOpts configures the preamble for a generated data transfer / download script.
type dataNomadScriptOpts struct {
	RunName       string
	PlacementNode string
	Driver        string
	Tool          string // selects OCI image when driver is docker/containerd
	// Artifacts lists binaries Nomad should fetch into the task directory before
	// the task starts (one entry per tool needed by the script).  Used by the
	// exec/raw_exec driver path to stage tool binaries from the cluster S3 bucket.
	Artifacts []downloadArtifact
	// ExtraConstraints are additional Nomad placement constraints in "<attr><op><val>"
	// format, passed as --constraint flags to abc job run.
	ExtraConstraints []string
}

// placementConstraintExpr returns the Nomad constraint expression for a node UUID or name.
// Returns empty string when node is blank.
func placementConstraintExpr(node string) string {
	node = strings.TrimSpace(node)
	if node == "" {
		return ""
	}
	if nomadNodeUUIDPattern.MatchString(node) {
		return fmt.Sprintf("node.unique.id==%s", node)
	}
	return fmt.Sprintf("node.unique.name==%s", node)
}

func submitDataNomadScript(cmd *cobra.Command, opts dataNomadScriptOpts, taskBody string) error {
	tmpScript, err := os.CreateTemp("", "abc-data-*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temp script: %w", err)
	}
	if os.Getenv("ABC_DEBUG_KEEP_SCRIPT") == "" {
		defer os.Remove(tmpScript.Name())
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "[abc-debug] keeping script at %s\n", tmpScript.Name())
	}

	if _, err := tmpScript.WriteString("#!/bin/sh\nset -euo pipefail\n"); err != nil {
		return fmt.Errorf("failed to write script header: %w", err)
	}
	if _, err := tmpScript.WriteString(taskBody); err != nil {
		return err
	}
	if err := tmpScript.Close(); err != nil {
		return fmt.Errorf("unable to close temp script: %w", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("unable to resolve executable path: %w", err)
	}

	image := defaultDockerImage
	if img, ok := dockerImageByTool[strings.ToLower(opts.Tool)]; ok {
		image = img
	}

	jobArgs := buildDataDownloadJobRunArgs(opts.Driver, image, opts.RunName, opts.Artifacts, tmpScript.Name())

	// Add placement constraints as CLI flags (before the script-path positional arg).
	// We cannot rely on #ABC preamble directives because set -euo pipefail in the
	// script header stops the preamble scanner before reaching any #ABC lines.
	var constraintArgs []string
	if expr := placementConstraintExpr(opts.PlacementNode); expr != "" {
		constraintArgs = append(constraintArgs, "--constraint", expr)
	}
	for _, c := range opts.ExtraConstraints {
		if c = strings.TrimSpace(c); c != "" {
			constraintArgs = append(constraintArgs, "--constraint", c)
		}
	}
	if len(constraintArgs) > 0 {
		// Insert before the last element (the script path positional arg).
		scriptPath := jobArgs[len(jobArgs)-1]
		jobArgs = append(jobArgs[:len(jobArgs)-1], constraintArgs...)
		jobArgs = append(jobArgs, scriptPath)
	}

	task := exec.Command(execPath, jobArgs...)
	task.Stdout = cmd.OutOrStdout()
	task.Stderr = cmd.ErrOrStderr()
	return task.Run()
}
