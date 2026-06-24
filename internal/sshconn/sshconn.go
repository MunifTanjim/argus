// Package sshconn provides a net.Conn-backed transport that tunnels a connection
// through a managed `ssh -W` child process. The node uses it to reach a gateway that
// binds loopback on a machine reachable over SSH, without exposing the gateway publicly:
// ssh carries the bytes (and provides encryption + auth), and its stdio is adapted
// to a net.Conn the WebSocket uplink dials over.
package sshconn

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"sync"
	"time"
)

// command builds the ssh invocation that forwards stdio to remoteHostPort on the
// SSH host. sshPort, when non-empty, sets the SSH port (ssh -p); empty defers to the
// user's ssh config / 22. It is a package var so tests can substitute a fake binary.
//
// BatchMode=yes makes a headless node fail fast instead of blocking on a password
// prompt (it relies on keys/agent); everything else defers to the user's ssh config.
var command = func(sshDest, remoteHostPort, sshPort string) *exec.Cmd {
	args := []string{"-W", remoteHostPort, "-o", "BatchMode=yes"}
	if sshPort != "" {
		args = append(args, "-p", sshPort)
	}
	args = append(args, sshDest)
	return exec.Command("ssh", args...)
}

// Dial spawns a fresh `ssh -W <remoteHostPort> <sshDest>` (with `-p <sshPort>` when
// sshPort is non-empty) and returns its stdio as a net.Conn. Callers wrap this in an
// http.Transport.DialContext closure (dropping the dialed network/address — ssh, not
// the caller, decides where the bytes go). ssh's stderr is streamed to log so auth and
// host-key failures surface. The process is detached from any request context and lives
// until the returned conn is closed, so the long-lived uplink outlives the short
// handshake request context.
func Dial(sshDest, remoteHostPort, sshPort string, log *slog.Logger) (net.Conn, error) {
	cmd := command(sshDest, remoteHostPort, sshPort)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ssh stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ssh stdin: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("ssh stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ssh: %w", err)
	}
	go logLines(stderr, log)

	return &execConn{r: stdout, w: stdin, cmd: cmd}, nil
}

// logLines streams a reader's lines to log at Warn (ssh writes diagnostics — auth
// failures, host-key prompts — to stderr).
func logLines(r io.Reader, log *slog.Logger) {
	if log == nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		log.Warn("ssh", "line", sc.Text())
	}
}

// execConn adapts a child process's stdio (and lifecycle) to net.Conn. Deadlines are
// no-ops: stdio pipes do not support them, and the WebSocket layer takes over the conn
// after the handshake. Close terminates the process.
type execConn struct {
	r         io.Reader
	w         io.WriteCloser
	cmd       *exec.Cmd
	closeOnce sync.Once
	closeErr  error
}

func (c *execConn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *execConn) Write(b []byte) (int, error) { return c.w.Write(b) }

func (c *execConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.w.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		c.closeErr = c.cmd.Wait()
	})
	return c.closeErr
}

func (c *execConn) LocalAddr() net.Addr             { return stubAddr{} }
func (c *execConn) RemoteAddr() net.Addr            { return stubAddr{} }
func (c *execConn) SetDeadline(time.Time) error     { return nil }
func (c *execConn) SetReadDeadline(time.Time) error { return nil }
func (c *execConn) SetWriteDeadline(time.Time) error {
	return nil
}

type stubAddr struct{}

func (stubAddr) Network() string { return "ssh" }
func (stubAddr) String() string  { return "ssh" }
