// Package executor runs commands and reads/writes files either on the local
// machine or on a remote Saltbox host over SSH/SFTP. All higher layers use the
// Executor interface so the backend works against local or remote unchanged.
package executor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Strip ANSI escape sequences (OSC, Fe, CSI) produced by PTY sessions.
var ansiRE = regexp.MustCompile(
	"\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC
		"|\x1b[@-Z\\\\-_]" + // Fe single-char
		"|\x1b\\[[0-?]*[ -/]*[@-~]") // CSI

func cleanPTY(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// Stream is the result of RunStream: read Lines until closed, then Exit().
type Stream struct {
	Lines <-chan string
	exit  chan int
}

// Exit blocks until the command finishes and returns its exit code.
func (s *Stream) Exit() int { return <-s.exit }

type Executor interface {
	// Run executes cmd and returns (exitCode, mergedStdoutStderr, error).
	// error is non-nil only for spawn/transport failures, not a non-zero exit.
	Run(ctx context.Context, cmd []string, cwd string) (int, string, error)
	// RunStdout is like Run but captures stdout only (stderr discarded).
	RunStdout(ctx context.Context, cmd []string, cwd string) (int, string, error)
	// RunStream streams output line-by-line; pty allocates a pseudo-TTY.
	RunStream(ctx context.Context, cmd []string, cwd string, pty bool) (*Stream, error)

	ReadFile(ctx context.Context, path string) (string, error)
	WriteFile(ctx context.Context, path, content string) error
	FileExists(ctx context.Context, path string) (bool, error)
	MakeDirs(ctx context.Context, path string) error
	// Upload streams src into the file at path (for large binary uploads).
	Upload(ctx context.Context, path string, src io.Reader) error
	// Download opens path for streaming reads; caller closes the reader.
	Download(ctx context.Context, path string) (io.ReadCloser, error)
	Close() error
}

func cmdExit(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// streamLines reads r, splitting on \n (and \r when pty), cleaning ANSI for pty,
// sending each line to out. Returns when r reaches EOF.
func streamLines(r io.Reader, pty bool, out chan<- string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	if pty {
		sc.Split(scanLinesCR)
	}
	for sc.Scan() {
		line := sc.Text()
		if pty {
			line = cleanPTY(line)
		}
		out <- line
	}
}

// scanLinesCR splits on \n or \r so progress redraws (bare CR) flow through.
func scanLinesCR(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// shJoin shell-quotes argv into a single command string (for SSH / `script`).
func shJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shQuote(a)
	}
	return strings.Join(parts, " ")
}

var safeArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	if safeArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func withCwd(cmdStr, cwd string) string {
	if cwd == "" {
		return cmdStr
	}
	return "cd " + shQuote(cwd) + " && " + cmdStr
}

// ── Local ────────────────────────────────────────────────────────────────────

type LocalExecutor struct{}

func (LocalExecutor) Run(ctx context.Context, cmd []string, cwd string) (int, string, error) {
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Dir = cwd
	out, err := c.CombinedOutput()
	if err != nil && cmdExit(err) < 0 {
		return -1, string(out), err
	}
	return cmdExit(err), string(out), nil
}

func (LocalExecutor) RunStdout(ctx context.Context, cmd []string, cwd string) (int, string, error) {
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Dir = cwd
	var stdout bytes.Buffer
	c.Stdout = &stdout
	err := c.Run()
	if err != nil && cmdExit(err) < 0 {
		return -1, stdout.String(), err
	}
	return cmdExit(err), stdout.String(), nil
}

func (LocalExecutor) RunStream(ctx context.Context, cmd []string, cwd string, pty bool) (*Stream, error) {
	args := cmd
	if pty {
		args = []string{"script", "-qfc", shJoin(cmd), "/dev/null"}
	}
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = cwd
	pr, pw := io.Pipe()
	c.Stdout = pw
	c.Stderr = pw
	if err := c.Start(); err != nil {
		return nil, err
	}
	lines := make(chan string, 64)
	exit := make(chan int, 1)
	exitCode := make(chan int, 1)
	go func() {
		err := c.Wait()
		_ = pw.Close() // unblock the reader
		exitCode <- cmdExit(err)
	}()
	go func() {
		streamLines(pr, pty, lines)
		close(lines)
		exit <- <-exitCode
	}()
	return &Stream{Lines: lines, exit: exit}, nil
}

func (LocalExecutor) ReadFile(_ context.Context, path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func (LocalExecutor) WriteFile(_ context.Context, path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func (LocalExecutor) FileExists(_ context.Context, path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (LocalExecutor) MakeDirs(_ context.Context, path string) error {
	return os.MkdirAll(path, 0o755)
}

func (LocalExecutor) Upload(_ context.Context, path string, src io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

func (LocalExecutor) Download(_ context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (LocalExecutor) Close() error { return nil }
