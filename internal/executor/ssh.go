package executor

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SSHExecutor runs commands and reads/writes files on a remote Saltbox host.
// The SSH client + SFTP client are created lazily and reused; a mutex guards
// (re)connection so a slow/unreachable host can't be dialled concurrently.
type SSHExecutor struct {
	host       string
	port       int
	user       string
	keyPath    string
	passphrase string
	password   string

	mu   sync.Mutex
	cli  *ssh.Client
	sftp *sftp.Client
}

func NewSSH(host string, port int, user, keyPath, passphrase, password string) *SSHExecutor {
	return &SSHExecutor{host: host, port: port, user: user, keyPath: keyPath, passphrase: passphrase, password: password}
}

func (e *SSHExecutor) clientConfig() (*ssh.ClientConfig, error) {
	cfg := &ssh.ClientConfig{
		User:            e.user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // matches asyncssh known_hosts=None
		Timeout:         15 * time.Second,            // TCP + handshake
	}
	if e.password != "" {
		cfg.Auth = []ssh.AuthMethod{ssh.Password(e.password)}
		return cfg, nil
	}
	key, err := os.ReadFile(e.keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", e.keyPath, err)
	}
	var signer ssh.Signer
	if e.passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(e.passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(key)
	}
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	return cfg, nil
}

// conn returns a live SSH client, dialling/reconnecting under the lock.
func (e *SSHExecutor) conn() (*ssh.Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cli != nil {
		return e.cli, nil
	}
	cfg, err := e.clientConfig()
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(e.host, strconv.Itoa(e.port))
	cli, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	e.cli = cli
	return cli, nil
}

func (e *SSHExecutor) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sftp != nil {
		_ = e.sftp.Close()
		e.sftp = nil
	}
	if e.cli != nil {
		_ = e.cli.Close()
		e.cli = nil
	}
}

func (e *SSHExecutor) sftpClient() (*sftp.Client, error) {
	e.mu.Lock()
	if e.sftp != nil {
		s := e.sftp
		e.mu.Unlock()
		return s, nil
	}
	e.mu.Unlock()
	cli, err := e.conn()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sftp == nil {
		s, err := sftp.NewClient(cli)
		if err != nil {
			return nil, err
		}
		e.sftp = s
	}
	return e.sftp, nil
}

// runOnce runs a command on a fresh session, capturing output.
// stdoutOnly discards stderr; otherwise stdout+stderr are merged.
func (e *SSHExecutor) runOnce(ctx context.Context, cmdStr string, stdoutOnly bool) (int, string, error) {
	cli, err := e.conn()
	if err != nil {
		return -1, "", err
	}
	sess, err := cli.NewSession()
	if err != nil {
		return -1, "", err
	}
	defer sess.Close()

	var buf writeBuf
	sess.Stdout = &buf
	if !stdoutOnly {
		sess.Stderr = &buf
	}

	done := make(chan error, 1)
	if err := sess.Start(cmdStr); err != nil {
		return -1, "", err
	}
	go func() { done <- sess.Wait() }()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		return -1, buf.String(), ctx.Err()
	case werr := <-done:
		return sshExit(werr), buf.String(), nil
	}
}

func (e *SSHExecutor) Run(ctx context.Context, cmd []string, cwd string) (int, string, error) {
	return e.retry(func() (int, string, error) {
		return e.runOnce(ctx, withCwd(shJoin(cmd), cwd), false)
	})
}

func (e *SSHExecutor) RunStdout(ctx context.Context, cmd []string, cwd string) (int, string, error) {
	return e.retry(func() (int, string, error) {
		return e.runOnce(ctx, withCwd(shJoin(cmd), cwd), true)
	})
}

// retry re-dials once on a transport error (dropped connection).
func (e *SSHExecutor) retry(fn func() (int, string, error)) (int, string, error) {
	rc, out, err := fn()
	if err != nil && isTransport(err) {
		e.reset()
		return fn()
	}
	return rc, out, err
}

func (e *SSHExecutor) RunStream(ctx context.Context, cmd []string, cwd string, pty bool) (*Stream, error) {
	cli, err := e.conn()
	if err != nil {
		return nil, err
	}
	sess, err := cli.NewSession()
	if err != nil {
		e.reset()
		return nil, err
	}
	if pty {
		_ = sess.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{})
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stderr, _ := sess.StderrPipe()

	cmdStr := withCwd(shJoin(cmd), cwd)
	if err := sess.Start(cmdStr); err != nil {
		sess.Close()
		return nil, err
	}

	lines := make(chan string, 64)
	exit := make(chan int, 1)
	go func() {
		// Read stdout + stderr concurrently. io.MultiReader would drain stdout
		// fully before touching stderr, so a long-running command that logs to
		// stderr (rclone's stats/progress) would show nothing until it exits.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); streamLines(stdout, pty, lines) }()
		if stderr != nil && !pty {
			wg.Add(1)
			go func() { defer wg.Done(); streamLines(stderr, false, lines) }()
		}
		wg.Wait()
		werr := sess.Wait()
		sess.Close()
		close(lines)
		exit <- sshExit(werr)
	}()
	// Stop the session if the context is cancelled. Send SIGINT first so rclone
	// (and friends) can shut down gracefully and clean up partial uploads; force
	// kill only if it hasn't exited shortly after.
	go func() {
		<-ctx.Done()
		_ = sess.Signal(ssh.SIGINT)
		time.AfterFunc(8*time.Second, func() {
			_ = sess.Signal(ssh.SIGKILL)
			_ = sess.Close()
		})
	}()
	return &Stream{Lines: lines, exit: exit}, nil
}

func (e *SSHExecutor) ReadFile(_ context.Context, path string) (string, error) {
	s, err := e.sftpClient()
	if err != nil {
		return "", err
	}
	f, err := s.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	return string(b), err
}

func (e *SSHExecutor) WriteFile(_ context.Context, path, content string) error {
	s, err := e.sftpClient()
	if err != nil {
		return err
	}
	f, err := s.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write([]byte(content))
	return err
}

func (e *SSHExecutor) Upload(_ context.Context, path string, src io.Reader) error {
	s, err := e.sftpClient()
	if err != nil {
		return err
	}
	f, err := s.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

func (e *SSHExecutor) Download(_ context.Context, path string) (io.ReadCloser, error) {
	s, err := e.sftpClient()
	if err != nil {
		return nil, err
	}
	return s.Open(path) // *sftp.File is an io.ReadCloser; caller closes
}

func (e *SSHExecutor) FileExists(_ context.Context, path string) (bool, error) {
	s, err := e.sftpClient()
	if err != nil {
		return false, err
	}
	_, err = s.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, nil
}

func (e *SSHExecutor) MakeDirs(_ context.Context, path string) error {
	s, err := e.sftpClient()
	if err != nil {
		return err
	}
	return s.MkdirAll(path)
}

func (e *SSHExecutor) Close() error {
	e.reset()
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func sshExit(err error) int {
	if err == nil {
		return 0
	}
	var ee *ssh.ExitError
	if asSSHExit(err, &ee) {
		return ee.ExitStatus()
	}
	return -1
}

func asSSHExit(err error, target **ssh.ExitError) bool {
	if e, ok := err.(*ssh.ExitError); ok {
		*target = e
		return true
	}
	return false
}

func isTransport(err error) bool {
	if err == nil {
		return false
	}
	// ssh.ExitError / ExitMissingError mean the command ran; anything else
	// (dial, EOF, closed) is treated as a transport failure worth one retry.
	if _, ok := err.(*ssh.ExitError); ok {
		return false
	}
	if _, ok := err.(*ssh.ExitMissingError); ok {
		return true
	}
	return true
}

// writeBuf is a tiny thread-safe-enough buffer (single writer per session).
type writeBuf struct{ b []byte }

func (w *writeBuf) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *writeBuf) String() string              { return string(w.b) }
