package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/term"
)

// Executor abstracts local and SSH command execution so install logic
// is identical regardless of transport.
type Executor interface {
	// Run executes a shell command on the target, writing stdout+stderr to w.
	Run(ctx context.Context, cmd string, w io.Writer) error
	// Upload copies r to remotePath on the target with the given permissions.
	Upload(ctx context.Context, r io.Reader, remotePath string, mode os.FileMode) error
	// OS returns "linux", "darwin", or "windows".
	OS() string
	// Arch returns "amd64", "arm64", etc.
	Arch() string
	// Close releases resources (no-op for localExec).
	Close() error
}

// ─── localExec ────────────────────────────────────────────────────────────────

type localExec struct {
	goos   string
	goarch string
}

func newLocalExec() *localExec {
	return &localExec{goos: runtime.GOOS, goarch: runtime.GOARCH}
}

func (l *localExec) OS() string   { return l.goos }
func (l *localExec) Arch() string { return l.goarch }
func (l *localExec) Close() error { return nil }

func (l *localExec) Run(_ context.Context, command string, w io.Writer) error {
	var sh, flag string
	if l.goos == "windows" {
		sh, flag = "cmd", "/C"
	} else {
		sh, flag = "/bin/sh", "-c"
	}
	c := exec.Command(sh, flag, command)
	c.Stdout = w
	c.Stderr = w
	return c.Run()
}

func (l *localExec) Upload(_ context.Context, r io.Reader, remotePath string, mode os.FileMode) error {
	if err := os.MkdirAll(dirOf(remotePath), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// ─── sshExec ──────────────────────────────────────────────────────────────────

// SSHConfig holds parameters for establishing an SSH connection.
type SSHConfig struct {
	Host    string
	Port    int
	User    string
	KeyFile string // empty → try default keys + SSH agent + password prompt

	// Password is used for both SSH password authentication and sudo -S during
	// install. When set, all `sudo` commands are transparently rewritten to pipe
	// the password through stdin (sudo -S), and the password is added to the SSH
	// auth chain so login works without key-based auth.
	//
	// Also read from the ABC_NODE_PASSWORD environment variable.
	Password string

	// Jump host (optional). When set, abc dials the jump host first and tunnels
	// the target connection through it — equivalent to `ssh -J jump target`.
	JumpHost    string
	JumpPort    int    // default: 22
	JumpUser    string // default: same as User
	JumpKeyFile string // default: same as KeyFile

	// SkipHostKeyCheck disables known_hosts verification (insecure; dev only).
	// Default (false): check against ~/.ssh/known_hosts, error on unknown hosts.
	SkipHostKeyCheck bool
}

type sshExec struct {
	client       *ssh.Client
	jumpClient   *ssh.Client // non-nil when a jump hop was used; closed in Close()
	goos         string
	goarch       string
	sudoPassword string // from SSHConfig.Password; injected into sudo calls via stdin
}

// newSSHExec connects to the remote host and returns a ready sshExec.
//
// Auth chain (hashi-up pattern):
//  1. Explicit key file (--ssh-key)
//  2. Default key files (~/.ssh/id_{rsa,ed25519,ecdsa})
//  3. SSH agent (SSH_AUTH_SOCK)
//  4. Keyboard-interactive (prompted in terminal)
//  5. Password prompt (last resort)
//
// Host key verification:
//   - Default: verify against ~/.ssh/known_hosts (errors on unknown/mismatched hosts)
//   - --skip-host-key-check: InsecureIgnoreHostKey (dev/testing only)
//
// Jump host:
//   - When --jump-host is set: dial jump → tunnel TCP → SSH handshake over tunnel
func newSSHExec(cfg SSHConfig) (*sshExec, error) {
	hostKeyCallback, err := buildHostKeyCallback(cfg.SkipHostKeyCheck)
	if err != nil {
		return nil, err
	}
	capturedPassword := ""
	targetAuths, err := buildSSHAuthMethods(cfg.KeyFile, cfg.User, cfg.Password, &capturedPassword)
	if err != nil {
		return nil, err
	}

	targetCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            targetAuths,
		HostKeyCallback: hostKeyCallback,
		Timeout:         30 * time.Second,
	}

	targetAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	var (
		client     *ssh.Client
		jumpClient *ssh.Client
	)

	if cfg.JumpHost != "" {
		// ── Two-hop dial: local → jump → target ───────────────────────────────
		jumpPort := cfg.JumpPort
		if jumpPort == 0 {
			jumpPort = 22
		}
		jumpUser := cfg.JumpUser
		if jumpUser == "" {
			jumpUser = cfg.User
		}
		jumpKeyFile := cfg.JumpKeyFile
		if jumpKeyFile == "" {
			jumpKeyFile = cfg.KeyFile
		}

		// Jump host uses the same password if provided (common for internal networks).
		jumpAuths, err := buildSSHAuthMethods(jumpKeyFile, jumpUser, cfg.Password, &capturedPassword)
		if err != nil {
			return nil, fmt.Errorf("SSH jump host auth: %w", err)
		}
		// Jump host key callback: use same policy as target (consistent UX).
		jumpHostKeyCallback, err := buildHostKeyCallback(cfg.SkipHostKeyCheck)
		if err != nil {
			return nil, err
		}
		jumpCfg := &ssh.ClientConfig{
			User:            jumpUser,
			Auth:            jumpAuths,
			HostKeyCallback: jumpHostKeyCallback,
			Timeout:         30 * time.Second,
		}
		jumpAddr := fmt.Sprintf("%s:%d", cfg.JumpHost, jumpPort)

		jumpClient, err = ssh.Dial("tcp", jumpAddr, jumpCfg)
		if err != nil {
			return nil, fmt.Errorf("SSH dial jump host %s: %w", jumpAddr, err)
		}

		// Tunnel a raw TCP connection through the jump host to the target.
		tunnelConn, err := jumpClient.Dial("tcp", targetAddr)
		if err != nil {
			jumpClient.Close()
			return nil, fmt.Errorf("SSH tunnel through %s to %s: %w", cfg.JumpHost, targetAddr, err)
		}

		// Run the SSH handshake over the tunnel.
		ncc, chans, reqs, err := ssh.NewClientConn(tunnelConn, cfg.Host, targetCfg)
		if err != nil {
			tunnelConn.Close()
			jumpClient.Close()
			return nil, fmt.Errorf("SSH handshake with %s (via %s): %w", cfg.Host, cfg.JumpHost, err)
		}
		client = ssh.NewClient(ncc, chans, reqs)

	} else {
		// ── Direct dial ───────────────────────────────────────────────────────
		client, err = ssh.Dial("tcp", targetAddr, targetCfg)
		if err != nil {
			return nil, fmt.Errorf("SSH dial %s: %w", targetAddr, err)
		}
	}

	goos, goarch, err := detectRemoteOSArch(client)
	if err != nil {
		client.Close()
		if jumpClient != nil {
			jumpClient.Close()
		}
		return nil, err
	}

	sudoPass := cfg.Password
	if sudoPass == "" && capturedPassword != "" {
		sudoPass = capturedPassword
	}

	return &sshExec{
		client:       client,
		jumpClient:   jumpClient,
		goos:         goos,
		goarch:       goarch,
		sudoPassword: sudoPass,
	}, nil
}

func (s *sshExec) OS() string   { return s.goos }
func (s *sshExec) Arch() string { return s.goarch }

func (s *sshExec) Close() error {
	err := s.client.Close()
	if s.jumpClient != nil {
		if e := s.jumpClient.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

func (s *sshExec) Run(ctx context.Context, command string, w io.Writer) error {
	sess, err := s.client.NewSession()
	if err != nil {
		return fmt.Errorf("new SSH session: %w", err)
	}
	defer sess.Close()
	sess.Stdout = w
	sess.Stderr = w

	// When a sudo password is configured, rewrite every `sudo ` occurrence in the
	// command to use `sudo -S -p ''` (read password from stdin, suppress prompt).
	// Feed a finite password stream sized for the number of sudo calls so the SSH
	// stdin copier can terminate cleanly once input is sent.
	if s.sudoPassword != "" && strings.Contains(command, "sudo ") {
		sudoCount := strings.Count(command, "sudo ")
		command = strings.ReplaceAll(command, "sudo ", "sudo -S -p '' ")
		sess.Stdin = newSudoPasswordReader(s.sudoPassword, sudoCount)
	}

	done := make(chan error, 1)
	go func() { done <- sess.Run(command) }()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGTERM)
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// Upload transfers r to remotePath on the remote host using SFTP.
// SFTP is binary-safe and avoids shell-escaping issues that cat-pipe approaches
// can have with paths or content containing special characters.
//
// When sudoPassword is set the destination may be a root-owned path (e.g.
// /etc/nomad.d, /usr/local/bin). In that case the file is written to a
// temporary location in /tmp (always user-writable) via SFTP and then moved
// to its final location with `sudo mv` + `sudo chmod`, which runs through
// the password-injecting Run() method.
func (s *sshExec) Upload(ctx context.Context, r io.Reader, remotePath string, mode os.FileMode) error {
	if s.sudoPassword != "" {
		return s.sudoUpload(ctx, r, remotePath, mode)
	}
	return s.sftpUpload(ctx, r, remotePath, mode)
}

// sftpUpload writes directly via SFTP (used when user owns the destination).
func (s *sshExec) sftpUpload(ctx context.Context, r io.Reader, remotePath string, mode os.FileMode) error {
	sc, err := sftp.NewClient(s.client)
	if err != nil {
		return fmt.Errorf("SFTP client: %w", err)
	}
	defer sc.Close()

	if err := sc.MkdirAll(dirOf(remotePath)); err != nil {
		return fmt.Errorf("SFTP mkdir %s: %w", dirOf(remotePath), err)
	}

	f, err := sc.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("SFTP open %s: %w", remotePath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("SFTP write %s: %w", remotePath, err)
	}
	if err := sc.Chmod(remotePath, mode); err != nil {
		return fmt.Errorf("SFTP chmod %s: %w", remotePath, err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

// sudoUpload writes to a /tmp staging path via SFTP (user-writable) and then
// uses sudo mv + chmod to place the file at its root-owned destination.
func (s *sshExec) sudoUpload(ctx context.Context, r io.Reader, remotePath string, mode os.FileMode) error {
	sc, err := sftp.NewClient(s.client)
	if err != nil {
		return fmt.Errorf("SFTP client: %w", err)
	}

	// Unique temp path in /tmp (always writable by the SSH user).
	tmpPath := fmt.Sprintf("/tmp/.abc-upload-%d", time.Now().UnixNano())

	f, err := sc.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		sc.Close()
		return fmt.Errorf("SFTP open tmp %s: %w", tmpPath, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		sc.Close()
		return fmt.Errorf("SFTP write tmp %s: %w", tmpPath, err)
	}
	f.Close()
	sc.Close()

	// Ensure destination directory exists and move into place.
	mvCmd := fmt.Sprintf(
		"sudo mkdir -p %s && sudo mv %s %s && sudo chmod %04o %s",
		dirOf(remotePath), tmpPath, remotePath, mode.Perm(), remotePath,
	)
	if err := s.Run(ctx, mvCmd, io.Discard); err != nil {
		// Best-effort cleanup of temp file.
		_ = s.Run(ctx, "rm -f "+tmpPath, io.Discard)
		return fmt.Errorf("sudo install %s: %w", remotePath, err)
	}
	return nil
}

// ─── Host key verification ────────────────────────────────────────────────────

// buildHostKeyCallback returns an ssh.HostKeyCallback appropriate for the config.
//
// When skipCheck is false (the default), it loads ~/.ssh/known_hosts and verifies
// each host against it. On first connection to an unknown host it prints a
// fingerprint prompt and offers to add it — similar to the OpenSSH client.
//
// When skipCheck is true it uses InsecureIgnoreHostKey (dev/testing only).
func buildHostKeyCallback(skipCheck bool) (ssh.HostKeyCallback, error) {
	if skipCheck {
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to insecure if we can't determine home dir.
		fmt.Fprintln(os.Stderr, "  warn: cannot determine home dir; skipping known_hosts check")
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec
	}

	khPath := filepath.Join(home, ".ssh", "known_hosts")

	// If known_hosts doesn't exist yet, create it and offer TOFU (trust on first use).
	if _, err := os.Stat(khPath); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(khPath), 0700); err == nil {
			_ = os.WriteFile(khPath, nil, 0600)
		}
	}

	callback, err := knownhosts.New(khPath)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts %s: %w", khPath, err)
	}

	// Wrap: on unknown host, prompt the user (TOFU) and add to known_hosts.
	return toFUCallback(khPath, callback), nil
}

// toFUCallback wraps a knownhosts callback with automatic Trust-On-First-Use.
// Known hosts are verified strictly; unknown hosts are added to known_hosts.
func toFUCallback(khPath string, strict ssh.HostKeyCallback) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := strict(hostname, remote, key)
		if err == nil {
			return nil // known and verified
		}

		// Key mismatch: possible MITM — hard error, never auto-accept.
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) > 0 {
			fmt.Fprintf(os.Stderr, "\n  !! WARNING: remote host identification has changed for %s\n", hostname)
			fmt.Fprintf(os.Stderr, "  !! This may indicate a man-in-the-middle attack.\n")
			fmt.Fprintf(os.Stderr, "  !! Expected key(s): %v\n", keyErr.Want)
			return err
		}

		// Unknown host: auto-accept and append to known_hosts.
		f, ferr := os.OpenFile(khPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if ferr != nil {
			return fmt.Errorf("update known_hosts: %w", ferr)
		}
		defer f.Close()
		entry := knownhosts.Line([]string{hostname}, key)
		if _, werr := fmt.Fprintln(f, entry); werr != nil {
			return fmt.Errorf("write known_hosts: %w", werr)
		}
		return nil
	}
}

// ─── SSH auth helpers ─────────────────────────────────────────────────────────

// newSudoPasswordReader returns a finite stdin stream containing the sudo
// password line repeated enough times to satisfy multiple sudo prompts.
//
// A finite stream is important: using an infinite reader can cause SSH session
// completion to surface as EOF when the remote side closes stdin.
func newSudoPasswordReader(password string, sudoCount int) io.Reader {
	if sudoCount < 1 {
		sudoCount = 1
	}
	// sudo typically allows up to 3 prompts per invocation.
	promptBudget := sudoCount * 3
	return strings.NewReader(strings.Repeat(password+"\n", promptBudget))
}

// buildSSHAuthMethods assembles the auth chain:
//  1. Explicit key file (or default keys)
//  2. SSH agent (SSH_AUTH_SOCK)
//  3. Explicit password (--password flag / ABC_NODE_PASSWORD env) — tried silently
//  4. Keyboard-interactive (for OTP / PAM challenges)
//  5. Interactive password prompt (last resort, only if no --password given)
//
// When captured is non-nil, any interactively typed password is written to
// *captured so callers can reuse it for sudo -S later in the install flow.
func buildSSHAuthMethods(keyFile, user, password string, captured *string) ([]ssh.AuthMethod, error) {
	var auths []ssh.AuthMethod

	// 1. Explicit key file
	if keyFile != "" {
		am, err := keyFileAuth(keyFile)
		if err != nil {
			return nil, fmt.Errorf("SSH key file %q: %w", keyFile, err)
		}
		auths = append(auths, am)
	} else {
		// 1b. Try default key locations (~/.ssh/id_rsa, id_ed25519, id_ecdsa)
		for _, kf := range defaultKeyFiles() {
			if am, err := keyFileAuth(kf); err == nil {
				auths = append(auths, am)
			}
		}
	}

	// 2. SSH agent (if SSH_AUTH_SOCK is set)
	if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
		if conn, err := net.Dial("unix", socket); err == nil {
			auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// 3. Explicit password (--password / ABC_NODE_PASSWORD) — tried silently,
	//    no prompt. Added before keyboard-interactive so it takes priority.
	if password != "" {
		auths = append(auths, ssh.Password(password))
	}

	// 4. Keyboard-interactive (handles OTP, PAM challenges, etc.)
	//    When an explicit password was given, answer single-question prompts
	//    with it automatically (covers systems that present password via
	//    keyboard-interactive rather than the password auth method).
	auths = append(auths, ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i, q := range questions {
			// If we have a password and the question looks like a password prompt,
			// answer with it automatically without blocking on a terminal read.
			if password != "" && !echos[i] {
				answers[i] = password
				continue
			}
			fmt.Fprintf(os.Stderr, "%s", q)
			if echos[i] {
				fmt.Fscan(os.Stdin, &answers[i])
			} else {
				pw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if err != nil {
					return nil, err
				}
				answers[i] = string(pw)
				if captured != nil && *captured == "" {
					*captured = string(pw)
				}
			}
		}
		return answers, nil
	}))

	// 5. Interactive password prompt (last resort — only when no --password given)
	if password == "" {
		auths = append(auths, ssh.PasswordCallback(func() (string, error) {
			fmt.Fprint(os.Stderr, "SSH password: ")
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if captured != nil {
				*captured = string(pw)
			}
			return string(pw), err
		}))
	}

	return auths, nil
}

func keyFileAuth(path string) (ssh.AuthMethod, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		// Encrypted key: prompt for passphrase.
		var ppErr *ssh.PassphraseMissingError
		if errors.As(err, &ppErr) {
			fmt.Fprintf(os.Stderr, "Passphrase for %s: ", path)
			pp, pErr := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if pErr != nil {
				return nil, pErr
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, pp)
			if err != nil {
				return nil, fmt.Errorf("decrypt key %s: %w", path, err)
			}
		} else {
			return nil, err
		}
	}
	return ssh.PublicKeys(signer), nil
}

func defaultKeyFiles() []string {
	home, _ := os.UserHomeDir()
	return []string{
		home + "/.ssh/id_rsa",
		home + "/.ssh/id_ed25519",
		home + "/.ssh/id_ecdsa",
	}
}

// detectRemoteOSArch runs `uname -sm` on the remote host and maps to Go OS/arch strings.
func detectRemoteOSArch(client *ssh.Client) (goos, goarch string, err error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer sess.Close()

	var buf strings.Builder
	sess.Stdout = &buf
	if err := sess.Run("uname -sm"); err != nil {
		return "", "", fmt.Errorf("uname -sm: %w", err)
	}

	parts := strings.Fields(strings.TrimSpace(buf.String()))
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected uname output: %q", buf.String())
	}

	switch strings.ToLower(parts[0]) {
	case "linux":
		goos = "linux"
	case "darwin":
		goos = "darwin"
	default:
		return "", "", fmt.Errorf("unsupported remote OS: %q (only linux/darwin supported over SSH)", parts[0])
	}

	switch strings.ToLower(parts[1]) {
	case "x86_64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	case "i386", "i686":
		goarch = "386"
	case "armv7l":
		goarch = "arm"
	default:
		return "", "", fmt.Errorf("unsupported remote arch: %q", parts[1])
	}

	return goos, goarch, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// dirOf returns the directory portion of a file path, handling both / and \ separators.
func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}

// ─── ~/.ssh/config resolution ─────────────────────────────────────────────────

// loadSSHConfigEntry reads ~/.ssh/config for the given host alias and returns
// an SSHConfig pre-populated with values from that block, plus a bool that is
// true when the alias resolves to a different Hostname (i.e. it is a real alias
// rather than a bare IP / FQDN that appears as-is in the config).
//
// Precedence: ~/.ssh/config values are used as defaults; the caller (runSSHAdd)
// overrides individual fields with any CLI flags that were explicitly set.
func loadSSHConfigEntry(alias string) (SSHConfig, bool) {
	cfg := SSHConfig{
		Host: alias, // default: alias is the real hostname
		Port: 22,
		User: os.Getenv("USER"),
	}
	if cfg.User == "" {
		cfg.User = "root"
	}

	hostname := ssh_config.Get(alias, "Hostname")
	isAlias := hostname != "" && hostname != alias
	if isAlias {
		cfg.Host = hostname
	}

	if port := ssh_config.Get(alias, "Port"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.Port = p
		}
	}

	if user := ssh_config.Get(alias, "User"); user != "" {
		cfg.User = user
	}

	if keys := ssh_config.GetAll(alias, "IdentityFile"); len(keys) > 0 {
		cfg.KeyFile = expandTilde(keys[0])
	}

	if pj := ssh_config.Get(alias, "ProxyJump"); pj != "" {
		parseProxyJump(pj, &cfg)
		// The ProxyJump value is often itself a ~/.ssh/config alias (e.g. "nomad00").
		// Resolve it recursively so we dial the real hostname/IP — the same thing
		// OpenSSH does internally. Without this, knownhosts checks the alias name
		// ("nomad00:22") rather than the actual IP ("100.108.199.30:22"), causing
		// spurious key-mismatch errors even when `ssh` itself connects fine.
		if cfg.JumpHost != "" {
			jumpEntry, _ := loadSSHConfigEntry(cfg.JumpHost)
			cfg.JumpHost = jumpEntry.Host // resolved hostname / IP
			if cfg.JumpPort == 22 && jumpEntry.Port != 22 {
				cfg.JumpPort = jumpEntry.Port
			}
			if cfg.JumpUser == "" && jumpEntry.User != "" {
				cfg.JumpUser = jumpEntry.User
			}
			if cfg.JumpKeyFile == "" && jumpEntry.KeyFile != "" {
				cfg.JumpKeyFile = jumpEntry.KeyFile
			}
		}
	}

	if shc := ssh_config.Get(alias, "StrictHostKeyChecking"); shc == "no" || shc == "off" {
		cfg.SkipHostKeyCheck = true
	}

	return cfg, isAlias
}

// expandTilde replaces a leading "~" with the user's home directory.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// parseProxyJump fills cfg.JumpHost/JumpPort/JumpUser from a ProxyJump value.
// Supported forms: host, user@host, host:port, user@host:port.
// Only the first hop is used when multiple are comma-separated.
func parseProxyJump(pj string, cfg *SSHConfig) {
	// Multi-hop (e.g. "bastion1,bastion2"): only the first hop is used.
	if idx := strings.IndexByte(pj, ','); idx >= 0 {
		pj = pj[:idx]
	}
	pj = strings.TrimSpace(pj)

	// Extract optional user@ prefix.
	user := ""
	if at := strings.LastIndex(pj, "@"); at >= 0 {
		user = pj[:at]
		pj = pj[at+1:]
	}

	host := pj
	port := 22
	// net.SplitHostPort handles "host:port" and "[ipv6]:port".
	if h, p, err := net.SplitHostPort(pj); err == nil {
		host = h
		if n, err2 := strconv.Atoi(p); err2 == nil {
			port = n
		}
	}

	cfg.JumpHost = host
	cfg.JumpPort = port
	if user != "" {
		cfg.JumpUser = user
	}
}

// LineWriter returns a writer that prefixes each output line with prefix.
func LineWriter(w io.Writer, prefix string) io.Writer {
	return &prefixWriter{w: w, prefix: prefix}
}

type prefixWriter struct {
	w      io.Writer
	prefix string
	buf    []byte
}

func (pw *prefixWriter) Write(p []byte) (int, error) {
	pw.buf = append(pw.buf, p...)
	for {
		idx := -1
		for i, b := range pw.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := pw.buf[:idx+1]
		pw.buf = pw.buf[idx+1:]
		fmt.Fprintf(pw.w, "%s%s", pw.prefix, line)
	}
	return len(p), nil
}
