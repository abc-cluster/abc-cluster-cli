package node

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
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
}

type sshExec struct {
	client *ssh.Client
	goos   string
	goarch string
}

// newSSHExec connects to the remote host and returns a ready sshExec.
// Auth chain (hashi-up pattern): key file → default keys → SSH agent → password prompt.
func newSSHExec(cfg SSHConfig) (*sshExec, error) {
	auths, err := buildSSHAuthMethods(cfg.KeyFile)
	if err != nil {
		return nil, err
	}

	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: use known_hosts in v2
		Timeout:         30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s: %w", addr, err)
	}

	goos, goarch, err := detectRemoteOSArch(client)
	if err != nil {
		client.Close()
		return nil, err
	}

	return &sshExec{client: client, goos: goos, goarch: goarch}, nil
}

func (s *sshExec) OS() string   { return s.goos }
func (s *sshExec) Arch() string { return s.goarch }
func (s *sshExec) Close() error { return s.client.Close() }

func (s *sshExec) Run(ctx context.Context, command string, w io.Writer) error {
	sess, err := s.client.NewSession()
	if err != nil {
		return fmt.Errorf("new SSH session: %w", err)
	}
	defer sess.Close()
	sess.Stdout = w
	sess.Stderr = w

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

// Upload uses an inline SCP-over-SSH trick: pipes r into `cat > remotePath`
// on the remote shell, avoiding the sftp/scp dependency. Sufficient for the
// small config files (< 4 KB) written during install.
func (s *sshExec) Upload(ctx context.Context, r io.Reader, remotePath string, mode os.FileMode) error {
	// Ensure parent directory exists.
	dir := dirOf(remotePath)
	if err := s.Run(ctx, fmt.Sprintf("mkdir -p %s", dir), io.Discard); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	sess, err := s.client.NewSession()
	if err != nil {
		return fmt.Errorf("new SSH session for upload: %w", err)
	}
	defer sess.Close()

	sess.Stdin = r
	if err := sess.Run(fmt.Sprintf("cat > %s && chmod %04o %s", remotePath, mode.Perm(), remotePath)); err != nil {
		return fmt.Errorf("upload to %s: %w", remotePath, err)
	}
	return nil
}

// ─── SSH auth helpers (hashi-up pattern) ─────────────────────────────────────

func buildSSHAuthMethods(keyFile string) ([]ssh.AuthMethod, error) {
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

	// 3. Interactive password prompt (last resort)
	auths = append(auths, ssh.PasswordCallback(func() (string, error) {
		fmt.Fprint(os.Stderr, "SSH password: ")
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(pw), err
	}))

	return auths, nil
}

func keyFileAuth(path string) (ssh.AuthMethod, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, err
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
