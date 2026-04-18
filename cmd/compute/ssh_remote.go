package compute

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// registerComputeSSHTransportFlags registers SSH/jump flags shared by
// `compute add --remote=…` and `compute node debug --remote=…`.
func registerComputeSSHTransportFlags(cmd *cobra.Command) {
	cmd.Flags().String("remote", "", "SSH target host or IP (Host alias from ~/.ssh/config is supported)")
	cmd.Flags().String("user", "", "SSH user (default: current OS user, or User from ~/.ssh/config)")
	cmd.Flags().String("ssh-key", "", "Path to SSH private key (default: ~/.ssh/id_rsa, then SSH agent)")
	cmd.Flags().Int("ssh-port", 22, "SSH port (default: 22)")
	cmd.Flags().Bool("skip-host-key-check", false, "Disable known_hosts verification (insecure; for dev/testing only)")
	cmd.Flags().String("password", "", "Node login password (SSH and sudo -S; or ABC_NODE_PASSWORD)")

	cmd.Flags().String("jump-host", "", "SSH jump/bastion host (ssh -J)")
	cmd.Flags().String("jump-user", "", "Username on the jump host (default: same as --user)")
	cmd.Flags().Int("jump-port", 22, "SSH port on the jump host (default: 22)")
	cmd.Flags().String("jump-key", "", "SSH private key for the jump host (default: same as --ssh-key)")
}

// sshExecutorFromRemoteFlags loads ~/.ssh/config, applies CLI overrides (same
// precedence as compute add), prints a short connection banner to out, and
// dials the remote host. The caller must Close() the executor when finished.
func sshExecutorFromRemoteFlags(ctx context.Context, cmd *cobra.Command, remote string, out io.Writer) (*sshExec, error) {
	sshCfg, isAlias := loadSSHConfigEntry(remote)

	if cmd.Flags().Changed("user") {
		sshCfg.User, _ = cmd.Flags().GetString("user")
	}
	if cmd.Flags().Changed("ssh-port") {
		sshCfg.Port, _ = cmd.Flags().GetInt("ssh-port")
	}
	if cmd.Flags().Changed("ssh-key") {
		sshCfg.KeyFile, _ = cmd.Flags().GetString("ssh-key")
	}
	if cmd.Flags().Changed("jump-host") {
		sshCfg.JumpHost, _ = cmd.Flags().GetString("jump-host")
	}
	if cmd.Flags().Changed("jump-user") {
		sshCfg.JumpUser, _ = cmd.Flags().GetString("jump-user")
	}
	if cmd.Flags().Changed("jump-port") {
		sshCfg.JumpPort, _ = cmd.Flags().GetInt("jump-port")
	}
	if cmd.Flags().Changed("jump-key") {
		sshCfg.JumpKeyFile, _ = cmd.Flags().GetString("jump-key")
	}
	if skip, _ := cmd.Flags().GetBool("skip-host-key-check"); skip {
		sshCfg.SkipHostKeyCheck = true
	}

	password, _ := cmd.Flags().GetString("password")
	if password == "" {
		password = os.Getenv("ABC_NODE_PASSWORD")
	}
	sshCfg.Password = password

	switch {
	case sshCfg.JumpHost != "":
		fmt.Fprintf(out, "\n  Connecting to %s@%s:%d via jump host %s...\n",
			sshCfg.User, remote, sshCfg.Port, sshCfg.JumpHost)
	case isAlias:
		fmt.Fprintf(out, "\n  Connecting to %s@%s:%d (resolved: %s:%d via ~/.ssh/config)...\n",
			sshCfg.User, remote, sshCfg.Port, sshCfg.Host, sshCfg.Port)
	default:
		fmt.Fprintf(out, "\n  Connecting to %s@%s:%d...\n", sshCfg.User, remote, sshCfg.Port)
	}

	ex, err := newSSHExec(ctx, sshCfg)
	if err != nil {
		return nil, err
	}
	return ex, nil
}
