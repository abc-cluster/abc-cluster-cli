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

// buildDataDownloadJobRunArgs builds argv for `abc job run --submit` for data-* Nomad wrappers.
func buildDataDownloadJobRunArgs(driver, image, jobName, scriptPath string) []string {
	nomadDriver := utils.NormalizeNomadTaskDriver(driver)
	args := []string{"job", "run", "--submit"}
	if n := strings.TrimSpace(jobName); n != "" {
		args = append(args, "--name", n)
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
}

func submitDataNomadScript(cmd *cobra.Command, opts dataNomadScriptOpts, taskBody string) error {
	tmpScript, err := os.CreateTemp("", "abc-data-*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temp script: %w", err)
	}
	defer os.Remove(tmpScript.Name())

	if _, err := tmpScript.WriteString("#!/bin/sh\nset -euo pipefail\n"); err != nil {
		return fmt.Errorf("failed to write script header: %w", err)
	}
	if opts.RunName != "" {
		if _, err := tmpScript.WriteString(fmt.Sprintf("#ABC --name=%s\n", opts.RunName)); err != nil {
			return err
		}
	}
	if line := placementConstraintPreamble(opts.PlacementNode); line != "" {
		if _, err := tmpScript.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	if _, err := tmpScript.WriteString(fmt.Sprintf("#ABC --driver=%s\n", utils.NormalizeNomadTaskDriver(opts.Driver))); err != nil {
		return err
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

	jobArgs := buildDataDownloadJobRunArgs(opts.Driver, image, opts.RunName, tmpScript.Name())

	task := exec.Command(execPath, jobArgs...)
	task.Stdout = cmd.OutOrStdout()
	task.Stderr = cmd.ErrOrStderr()
	return task.Run()
}
