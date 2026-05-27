package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SSHConfigBlock returns the SSH config stanza for a workbench VM.
// vmName is the Host alias (e.g. "wb-abhi").
// hostIP is the VM's private IP on the hypervisor (e.g. "192.168.64.5").
// jumpHost is the SSH alias for the jump host (e.g. "sun-aither").
func SSHConfigBlock(vmName, hostIP, jumpHost string) string {
	return fmt.Sprintf(`Host %s
    HostName %s
    User ubuntu
    ProxyJump %s
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
`, vmName, hostIP, jumpHost)
}

// SSHConfigPresent reports whether a Host block for vmName is already present
// in ~/.ssh/config. Returns false on any read error.
func SSHConfigPresent(vmName string) bool {
	cfgPath := sshConfigPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "Host "+vmName+"\n") ||
		strings.Contains(string(data), "Host "+vmName+" ")
}

// WriteSSHConfigEntry adds or replaces the SSH config stanza for vmName in
// ~/.ssh/config. It is safe to call on every start — if the block already
// exists with a different HostName (e.g. VM was recreated), the HostName is
// updated in place.
func WriteSSHConfigEntry(vmName, hostIP, jumpHost string) error {
	sshDir := filepath.Join(os.Getenv("HOME"), ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("mkdir ~/.ssh: %w", err)
	}
	cfgPath := sshConfigPath()

	var existing []byte
	existing, err := os.ReadFile(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", cfgPath, err)
	}

	block := SSHConfigBlock(vmName, hostIP, jumpHost)
	header := "Host " + vmName
	content := string(existing)

	if idx := strings.Index(content, header); idx >= 0 {
		// Replace the existing block.
		// A block runs from its "Host" line to the next "Host" line (or EOF).
		after := content[idx+len(header):]
		endOffset := strings.Index(after, "\nHost ")
		var blockEnd int
		if endOffset >= 0 {
			blockEnd = idx + len(header) + endOffset + 1 // keep the leading \n of next block
		} else {
			blockEnd = len(content)
		}
		content = content[:idx] + block + content[blockEnd:]
	} else {
		// Append after a blank line separator.
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + block
	}

	return os.WriteFile(cfgPath, []byte(content), 0600)
}

func sshConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), ".ssh", "config")
}
