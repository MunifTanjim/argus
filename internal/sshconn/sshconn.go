// Package sshconn provides a net.Conn that tunnels through a managed `ssh -W` child
// process. The node uses it to reach a loopback-bound gateway over SSH without
// exposing it publicly: ssh carries the bytes (with its encryption + auth) and its
// stdio is adapted to a net.Conn the WebSocket uplink dials over.
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

// command builds the ssh invocation forwarding stdio to remoteHostPort. A package var
// so tests can substitute a fake binary. BatchMode=yes makes a headless node fail
// fast (relying on keys/agent) instead of blocking on a password prompt.
var command = func(sshDest, remoteHostPort, sshPort string) *exec.Cmd {
	args := []string{"-W", remoteHostPort, "-o", "BatchMode=yes"}
	if sshPort != "" {
		args = append(args, "-p", sshPort)
	}
	args = append(args, sshDest)
	return exec.Command("ssh", args...)
}

// Dial spawns `ssh -W <remoteHostPort> <sshDest>` and returns its stdio as a net.Conn.
// Callers wrap this in an http.Transport.DialContext closure (ssh decides where the
// bytes go, so the dialed address is dropped). The process is detached from any request
// context and lives until the conn is closed, so the long-lived uplink outlives the
// handshake request context. ssh's stderr is logged so auth/host-key failures surface.
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

// logLines streams a reader's lines to log at Warn (ssh writes diagnostics to stderr).
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
