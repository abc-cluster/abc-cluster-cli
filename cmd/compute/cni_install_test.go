package compute

import (
	"strings"
	"testing"
)

func TestCNIPluginInstallSteps_RemovesTarballReadmeFiles(t *testing.T) {
	t.Parallel()
	steps, err := cniPluginInstallSteps("amd64", "1.6.2")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(steps, " && ")
	if !strings.Contains(joined, "sudo rm -f "+cniPluginsInstallDir+"/LICENSE") {
		t.Fatalf("expected LICENSE cleanup in steps:\n%s", joined)
	}
	if !strings.Contains(joined, cniPluginsInstallDir+"/README.md") {
		t.Fatalf("expected README.md cleanup in steps:\n%s", joined)
	}
}

func TestCNISanitizeInstallDirCmd(t *testing.T) {
	t.Parallel()
	cmd := cniSanitizeInstallDirCmd()
	if want := "sudo rm -f " + cniPluginsInstallDir + "/LICENSE"; !strings.Contains(cmd, want) {
		t.Fatalf("sanitize cmd: %q", cmd)
	}
}

func TestBridgeKernelModuleSetupCmd(t *testing.T) {
	t.Parallel()
	cmd := bridgeKernelModuleSetupCmd()
	if !strings.Contains(cmd, "modprobe bridge") {
		t.Fatalf("expected modprobe bridge: %q", cmd)
	}
	if !strings.Contains(cmd, nomadBridgeModuleLoadFile) {
		t.Fatalf("expected modules-load path: %q", cmd)
	}
}
