package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/logbuf"
)

var managedBin = os.Executable

const (
	managedStopTimeout  = 5 * time.Second
	managedReadyTimeout = 10 * time.Second
	managedLogTail      = 20
)

// spawnSpec runs an isolated node by default. A gatewayURL uplinks the node to
// that gateway, and a listenAddr co-locates a gateway with it.
type spawnSpec struct {
	socket     string
	gatewayURL string
	listenAddr string // a concrete port
	token      string
	configArgs []string
}

// args sets every topology key explicitly, so a mode, token, gateway URL, or
// tunnel in the config file or the environment cannot change what the child runs.
func (s spawnSpec) args() []string {
	a := []string{"start", "--argus-managed", "--socket", s.socket, "--tunnel="}
	a = append(a, s.configArgs...)
	if s.listenAddr != "" {
		a = append(a, "--mode=", "--gateway=", "--listen-addr="+s.listenAddr)
	} else {
		a = append(a, "--mode=node", "--gateway="+s.gatewayURL)
	}
	if s.token == "" {
		a = append(a, "--token=")
	}
	return a
}

// env carries a non-empty token, because ps shows argv.
func (s spawnSpec) env(environ []string) []string {
	env := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "ARGUS_TOKEN=") {
			env = append(env, kv)
		}
	}
	if s.token != "" {
		env = append(env, "ARGUS_TOKEN="+s.token)
	}
	return env
}

// configArgs gives the managed server the TUI's config file choice. Without
// either flag, the child resolves $ARGUS_CONFIG or the default as the TUI did.
func configArgs(cmd *cobra.Command) []string {
	if noConfig, _ := cmd.Flags().GetBool("no-config"); noConfig {
		return []string{"--no-config"}
	}
	if f := cmd.Flags().Lookup("config"); f != nil && f.Changed {
		return []string{"--config", f.Value.String()}
	}
	return nil
}

// resolveListenAddr checks that addr is free before the TUI takes the screen,
// and turns port 0 into a concrete port so that a restarted server binds the
// same one.
func resolveListenAddr(addr string) (string, *net.TCPAddr, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	bound := ln.Addr().(*net.TCPAddr)
	ln.Close()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", nil, err
	}
	if port == "0" {
		addr = net.JoinHostPort(host, strconv.Itoa(bound.Port))
	}
	return addr, bound, nil
}

type managedServer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *os.File
	logs   *logbuf.Buffer
	copied chan struct{}
	done   chan struct{}
	err    error // valid once done is closed
	exited chan error
}

func startManaged(spec spawnSpec) (*managedServer, error) {
	bin, err := managedBin()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, spec.args()...)
	cmd.Env = spec.env(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	w.Close()
	s := &managedServer{
		cmd:    cmd,
		stdin:  stdin,
		out:    r,
		logs:   logbuf.New(1000),
		copied: make(chan struct{}),
		done:   make(chan struct{}),
		exited: make(chan error, 1),
	}
	go func() {
		defer close(s.copied)
		copyLines(s.logs, s.out)
		s.out.Close()
	}()
	go func() {
		s.err = cmd.Wait()
		close(s.done)
		s.exited <- s.err
	}()
	return s, nil
}

// startManagedReady refuses a socket that another node serves, because
// waitReady cannot tell that node from the child.
func startManagedReady(spec spawnSpec) (*managedServer, error) {
	running, err := localNodeRunning(spec.socket)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, fmt.Errorf("a node is already running at %s", spec.socket)
	}
	s, err := startManaged(spec)
	if err != nil {
		return nil, err
	}
	if err := s.waitReady(spec.socket); err != nil {
		s.stop(managedStopTimeout)
		return nil, err
	}
	return s, nil
}

func (s *managedServer) Logs() *logbuf.Buffer {
	if s == nil {
		return nil
	}
	return s.logs
}

// Exited receives the exit status once. server.restart keeps the PID, so a
// restart does not count as an exit.
func (s *managedServer) Exited() <-chan error {
	if s == nil {
		return nil
	}
	return s.exited
}

// waitReady waits for the node socket. A co-located gateway listens before the
// node binds its socket, so the socket also covers the gateway.
func (s *managedServer) waitReady(socket string) error {
	deadline := time.Now().Add(managedReadyTimeout)
	for {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-s.done:
			return s.exitError()
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server did not start: %w:\n%s", err, s.logTail())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// exitError waits briefly for the output copy, so that the last lines hold
// the child's own error message.
func (s *managedServer) exitError() error {
	select {
	case <-s.copied:
	case <-time.After(time.Second):
	}
	status := "exit status 0"
	if s.err != nil {
		status = s.err.Error()
	}
	return fmt.Errorf("server exited (%s):\n%s", status, s.logTail())
}

func (s *managedServer) logTail() string {
	lines := s.logs.LinesRange(s.logs.Len()-managedLogTail, managedLogTail)
	return ansi.Strip(strings.Join(lines, "\n"))
}

// copyLines writes whole lines only: the TUI's own log output shares dst, and a
// partial line would merge with it. A last line without a newline gets one.
func copyLines(dst io.Writer, src io.Reader) {
	r := bufio.NewReader(src)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] != '\n' {
				line = append(line, '\n')
			}
			_, _ = dst.Write(line)
		}
		if err != nil {
			return
		}
	}
}

// stop closes stdin so that the server shuts down on its normal path, and
// kills its process group if it is still running after timeout.
func (s *managedServer) stop(timeout time.Duration) {
	if s == nil {
		return
	}
	s.stdin.Close()
	select {
	case <-s.done:
	case <-time.After(timeout):
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		<-s.done
	}
}
