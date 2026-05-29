package workbench

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// NodeSSH holds the SSH alias for a cluster node (e.g. "sun-aither").
type NodeSSH struct{ Host string }

// NodeSSHFromName derives the SSH alias for a Nomad node name.
// Convention: "aither" → "sun-aither".
func NodeSSHFromName(name string) NodeSSH { return NodeSSH{"sun-" + name} }

// VMState is the reported state of a Multipass VM.
type VMState string

const (
	VMStateRunning   VMState = "Running"
	VMStateStopped   VMState = "Stopped"
	VMStateSuspended VMState = "Suspended"
	VMStateUnknown   VMState = ""
)

type mpInfoEntry struct {
	IPv4  []string `json:"ipv4"`
	State string   `json:"state"`
}

type mpInfoDoc struct {
	Info map[string]mpInfoEntry `json:"info"`
}

// runOnNode executes a command on the node via SSH and returns combined output.
func runOnNode(node NodeSSH, args ...string) (string, error) {
	full := append([]string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=30",
		"-o", "StrictHostKeyChecking=no",
		node.Host,
	}, args...)
	out, err := exec.Command("ssh", full...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// RunOnNodePublic is the exported version of runOnNode. It executes a command
// on the node via SSH and returns combined output. Callers outside this package
// (e.g. cmd/admin/workbench) use this to run arbitrary commands on a node.
func RunOnNodePublic(node NodeSSH, args ...string) (string, error) {
	return runOnNode(node, args...)
}

// runOnNodeStream executes a command streaming output to w.
func runOnNodeStream(node NodeSSH, w io.Writer, args ...string) error {
	full := append([]string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=30",
		"-o", "StrictHostKeyChecking=no",
		node.Host,
	}, args...)
	cmd := exec.Command("ssh", full...)
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}

// GetVMState returns the current state of a Multipass VM.
// Returns VMStateUnknown if the VM does not exist.
func GetVMState(node NodeSSH, vmName string) (VMState, error) {
	out, err := runOnNode(node, "multipass", "info", "--format", "json", vmName)
	if err != nil {
		if strings.Contains(out, "does not exist") || strings.Contains(out, "not found") {
			return VMStateUnknown, nil
		}
		return VMStateUnknown, fmt.Errorf("multipass info: %w\n%s", err, out)
	}
	var doc mpInfoDoc
	if e := json.Unmarshal([]byte(out), &doc); e != nil {
		return VMStateUnknown, fmt.Errorf("parse multipass info: %w", e)
	}
	entry, ok := doc.Info[vmName]
	if !ok {
		return VMStateUnknown, nil
	}
	return VMState(entry.State), nil
}

// GetVMIP returns the primary IPv4 of a running VM.
func GetVMIP(node NodeSSH, vmName string) (string, error) {
	out, err := runOnNode(node, "multipass", "info", "--format", "json", vmName)
	if err != nil {
		return "", fmt.Errorf("multipass info: %w\n%s", err, out)
	}
	var doc mpInfoDoc
	if e := json.Unmarshal([]byte(out), &doc); e != nil {
		return "", fmt.Errorf("parse multipass info: %w", e)
	}
	entry, ok := doc.Info[vmName]
	if !ok || len(entry.IPv4) == 0 {
		return "", fmt.Errorf("no IPv4 address for VM %q (state: %s)", vmName, doc.Info[vmName].State)
	}
	return entry.IPv4[0], nil
}

// LaunchVM creates a new Multipass VM.
func LaunchVM(node NodeSSH, vmName string, cpus, memMB int, w io.Writer) error {
	memG := memMB / 1024
	if memG < 1 {
		memG = 1
	}
	fmt.Fprintf(w, "  Launching VM %s (%d CPUs, %dG RAM, 40G disk)...\n", vmName, cpus, memG)
	out, err := runOnNode(node,
		"multipass", "launch", "24.04",
		"--name", vmName,
		"--cpus", fmt.Sprintf("%d", cpus),
		"--memory", fmt.Sprintf("%dG", memG),
		"--disk", "40G",
	)
	if err != nil {
		return fmt.Errorf("multipass launch: %w\n%s", err, out)
	}
	fmt.Fprintf(w, "  VM launched.\n")
	return nil
}

// MountHomedir bind-mounts the user's data homedir into the VM.
// hostPath is on the node (e.g. /data/workbench/abhi/home).
// vmPath is inside the VM (e.g. /home/ubuntu).
func MountHomedir(node NodeSSH, vmName, hostPath, vmPath string) error {
	// Ensure host path exists.
	if _, err := runOnNode(node, "mkdir", "-p", hostPath); err != nil {
		return fmt.Errorf("mkdir %s: %w", hostPath, err)
	}
	out, err := runOnNode(node, "multipass", "mount", hostPath, vmName+":"+vmPath)
	if err != nil {
		if strings.Contains(out, "already mounted") || strings.Contains(out, "already exists") {
			return nil // idempotent
		}
		return fmt.Errorf("multipass mount: %w\n%s", err, out)
	}
	return nil
}

// StartVM resumes a suspended (or starts a stopped) VM.
func StartVM(node NodeSSH, vmName string) error {
	out, err := runOnNode(node, "multipass", "start", vmName)
	if err != nil {
		return fmt.Errorf("multipass start: %w\n%s", err, out)
	}
	return nil
}

// SuspendVM suspends a running VM (preserves state; ~5-10s to resume).
func SuspendVM(node NodeSSH, vmName string) error {
	out, err := runOnNode(node, "multipass", "suspend", vmName)
	if err != nil {
		return fmt.Errorf("multipass suspend: %w\n%s", err, out)
	}
	return nil
}

// WaitForSSH polls until the VM's SSH daemon accepts connections (max 60s).
func WaitForSSH(node NodeSSH, vmName string) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, err := runOnNode(node, "multipass", "exec", vmName, "--", "true")
		if err == nil {
			_ = out
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("VM %q SSH not ready after 60s", vmName)
}

// injectKeyTmpl embeds the base64-encoded public key directly in the script
// so there are no quoting issues passing it through SSH argument handling.
// base64 output contains only [A-Za-z0-9+/=] — safe to single-quote in bash.
var injectKeyTmpl = template.Must(template.New("inject-key").Parse(`#!/usr/bin/env bash
set -euo pipefail
KEY=$(echo '{{.KeyB64}}' | base64 -d)
mkdir -p /home/ubuntu/.ssh
chmod 700 /home/ubuntu/.ssh
grep -qxF "$KEY" /home/ubuntu/.ssh/authorized_keys 2>/dev/null \
  || echo "$KEY" >> /home/ubuntu/.ssh/authorized_keys
chmod 600 /home/ubuntu/.ssh/authorized_keys
`))

// InjectSSHKey adds the local machine's SSH public key to the VM's
// authorized_keys so the user can SSH in with their normal identity.
// Tries ~/.ssh/id_ed25519.pub, id_rsa.pub, id_ecdsa.pub in order; skips
// silently if no key file is found.
func InjectSSHKey(node NodeSSH, vmName string) error {
	pubKey := readFirstPubKey()
	if pubKey == "" {
		return nil
	}

	var buf bytes.Buffer
	if err := injectKeyTmpl.Execute(&buf, map[string]string{
		"KeyB64": base64.StdEncoding.EncodeToString([]byte(pubKey)),
	}); err != nil {
		return fmt.Errorf("render inject-key script: %w", err)
	}

	full := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=30",
		"-o", "StrictHostKeyChecking=no",
		node.Host,
		"multipass", "exec", vmName, "--", "bash", "-s",
	}
	cmd := exec.Command("ssh", full...)
	cmd.Stdin = &buf
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// readFirstPubKey returns the content of the first local SSH public key found.
func readFirstPubKey() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".ssh", "id_ed25519.pub"),
		filepath.Join(home, ".ssh", "id_rsa.pub"),
		filepath.Join(home, ".ssh", "id_ecdsa.pub"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// ProvisionVM runs the idempotent provision script inside the VM.
// Safe to call on an already-provisioned VM.
func ProvisionVM(node NodeSSH, vmName string, w io.Writer) error {
	fmt.Fprintf(w, "  Provisioning tools (first time, ~3-5 min)...\n")
	full := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=60",
		"-o", "StrictHostKeyChecking=no",
		node.Host,
		"multipass", "exec", vmName, "--", "bash", "-s",
	}
	cmd := exec.Command("ssh", full...)
	cmd.Stdin = strings.NewReader(vmProvisionScript)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("provision VM: %w", err)
	}
	return nil
}

// startServicesTmpl starts code-server and Jupyter Server inside the VM.
// Token is shell-quoted via the quote template function.
var startServicesTmpl = template.Must(
	template.New("start-services").
		Funcs(template.FuncMap{
			// quote produces a shell-safe double-quoted string (same as Go %q).
			"quote": func(s string) string { return fmt.Sprintf("%q", s) },
		}).
		Parse(`#!/usr/bin/env bash
set -euo pipefail
export PATH="$HOME/.pixi/bin:$HOME/.local/bin:$PATH"

# Stop stale instances gracefully.
pkill -f 'code-server' 2>/dev/null || true
pkill -f 'jupyter server' 2>/dev/null || true
sleep 1

# Jupyter Server.
nohup jupyter server \
    --no-browser --ip=0.0.0.0 --port=8888 \
    --ServerApp.token={{.Token | quote}} \
    --ServerApp.password="" \
    > /tmp/jupyter.log 2>&1 &

# code-server.
PASSWORD={{.Token | quote}} nohup code-server \
    --bind-addr 0.0.0.0:8080 \
    --auth password \
    > /tmp/code-server.log 2>&1 &

sleep 3
CS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080 2>/dev/null || echo "ERR")
JP=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8888 2>/dev/null || echo "ERR")
echo "  code-server: $CS  jupyter: $JP"
`))

// StartWorkbenchServices starts code-server and Jupyter Server inside the VM.
// Idempotent — kills stale instances first.
func StartWorkbenchServices(node NodeSSH, vmName, token string, w io.Writer) error {
	fmt.Fprintf(w, "  Starting workbench services...\n")

	var buf bytes.Buffer
	if err := startServicesTmpl.Execute(&buf, map[string]string{"Token": token}); err != nil {
		return fmt.Errorf("render start-services script: %w", err)
	}

	full := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=30",
		"-o", "StrictHostKeyChecking=no",
		node.Host,
		"multipass", "exec", vmName, "--", "bash", "-s",
	}
	cmd := exec.Command("ssh", full...)
	cmd.Stdin = &buf
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}

// IsWorkbenchHealthy checks that code-server and Jupyter are responding inside the VM.
func IsWorkbenchHealthy(node NodeSSH, vmName string) (codeServerOK, jupyterOK bool) {
	csOut, _ := runOnNode(node, "multipass", "exec", vmName, "--",
		"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "http://localhost:8080")
	jOut, _ := runOnNode(node, "multipass", "exec", vmName, "--",
		"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "http://localhost:8888")
	codeServerOK = strings.TrimSpace(csOut) == "200" || strings.TrimSpace(csOut) == "302"
	jupyterOK = strings.TrimSpace(jOut) == "200" || strings.TrimSpace(jOut) == "302"
	return
}

// vmProvisionScript is the idempotent bootstrap script for a workbench VM.
// Installs pixi, code-server, Jupyter Server, and configures PATH.
const vmProvisionScript = `#!/usr/bin/env bash
# abc-workbench provision v1 — idempotent
set -euo pipefail

echo "--- abc-workbench provision ---"

export DEBIAN_FRONTEND=noninteractive
sudo apt-get update -qq 2>&1 | tail -2
sudo apt-get install -y -qq curl git build-essential python3-pip python3-venv ca-certificates 2>&1 | tail -3

# Jupyter Server — install into a dedicated venv to avoid PEP 668 issues
# on Ubuntu 24.04's externally-managed Python.
echo "Installing Jupyter..."
if [ ! -d "$HOME/.venv/jupyter" ]; then
    python3 -m venv "$HOME/.venv/jupyter" 2>&1 | tail -2
fi
"$HOME/.venv/jupyter/bin/pip" install --quiet jupyterlab jupyter-server 2>&1 | tail -3
# Expose jupyter on PATH via a thin wrapper.
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/jupyter" << 'WRAPPER'
#!/usr/bin/env bash
exec "$HOME/.venv/jupyter/bin/jupyter" "$@"
WRAPPER
chmod +x "$HOME/.local/bin/jupyter"
echo "  Jupyter done"

# pixi
if ! command -v pixi &>/dev/null && ! [ -f "$HOME/.pixi/bin/pixi" ]; then
    echo "Installing pixi..."
    curl -fsSL https://pixi.sh/install.sh | bash 2>&1 | tail -3
    echo "  pixi done"
else
    echo "  pixi already present"
fi
grep -qxF 'export PATH="$HOME/.pixi/bin:$PATH"' ~/.bashrc || \
    echo 'export PATH="$HOME/.pixi/bin:$PATH"' >> ~/.bashrc

# code-server
if ! command -v code-server &>/dev/null; then
    echo "Installing code-server..."
    curl -fsSL https://code-server.dev/install.sh | sh -s -- --method standalone --prefix /usr/local 2>&1 | tail -5
    echo "  code-server done"
else
    echo "  code-server already present: $(code-server --version | head -1)"
fi

# Ensure SSH is enabled (should be default in ubuntu:24.04 cloud image)
sudo systemctl enable --now ssh 2>/dev/null || true

echo "--- provision complete ---"
`
