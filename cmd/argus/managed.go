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
	"sync/atomic"
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

type spawnRole int

const (
	roleNode spawnRole = iota
	roleGateway
)

// spawnSpec runs one managed server. A node is isolated, or uplinks to
// gatewayURL. A gateway listens on listenAddr.
type spawnSpec struct {
	role       spawnRole
	socket     string
	gatewayURL string
	listenAddr string // a concrete port
	token      string
	configArgs []string
	extraArgs  []string // last, so that they win over the defaults in args
	extraEnv   []string // for values that ps must not show
}

// args sets every topology key explicitly, so a mode, token, gateway URL, or
// tunnel in the config file or the environment cannot change what the child runs.
func (s spawnSpec) args() []string {
	a := []string{"start", "--argus-managed", "--socket", s.socket, "--tunnel="}
	a = append(a, s.configArgs...)
	if s.role == roleGateway {
		a = append(a, "--mode=gateway", "--gateway=", "--listen-addr="+s.listenAddr)
	} else {
		a = append(a, "--mode=node", "--gateway="+s.gatewayURL)
	}
	if s.token == "" {
		a = append(a, "--token=")
	}
	return append(a, s.extraArgs...)
}

func (s spawnSpec) readySocket() string {
	if s.role == roleGateway {
		return gatewaySocketPath(s.socket)
	}
	return s.socket
}

// probeReady covers a gateway's TCP port too, because the gateway binds it
// before its unix socket.
func (s spawnSpec) probeReady() error {
	conn, err := net.Dial("unix", s.readySocket())
	if err != nil {
		return err
	}
	return conn.Close()
}

// env carries a non-empty token, because ps shows argv. A node runs no
// tunnel, so it does not get the Cloudflare token.
func (s spawnSpec) env(environ []string) []string {
	env := make([]string, 0, len(environ)+1+len(s.extraEnv))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if k == "ARGUS_TOKEN" || (s.role == roleNode && k == "ARGUS_CLOUDFLARE_TOKEN") {
			continue
		}
		env = append(env, kv)
	}
	if s.token != "" {
		env = append(env, "ARGUS_TOKEN="+s.token)
	}
	return append(env, s.extraEnv...)
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
}

func startManaged(spec spawnSpec, out io.Writer) (*managedServer, error) {
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
		logs:   logbuf.New(managedLogTail),
		copied: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(s.copied)
		var dst io.Writer = s.logs
		if out != nil {
			dst = io.MultiWriter(s.logs, out)
		}
		copyLines(dst, s.out)
		s.out.Close()
	}()
	go func() {
		s.err = cmd.Wait()
		close(s.done)
	}()
	return s, nil
}

// startManagedReady refuses a socket that another argus serves, because
// waitReady cannot tell that server from the child.
func startManagedReady(spec spawnSpec, out io.Writer) (*managedServer, error) {
	if err := refuseRunning(spec.readySocket()); err != nil {
		return nil, err
	}
	s, err := startManaged(spec, out)
	if err != nil {
		return nil, err
	}
	if err := s.waitReady(spec); err != nil {
		s.stop(managedStopTimeout)
		return nil, err
	}
	return s, nil
}

// wait returns the exit status. server.restart keeps the PID, so a restart
// does not count as an exit.
func (s *managedServer) wait() error {
	<-s.done
	return s.err
}

func (s *managedServer) waitReady(spec spawnSpec) error {
	deadline := time.Now().Add(managedReadyTimeout)
	for {
		err := spec.probeReady()
		if err == nil {
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

// waitCopied waits briefly for the output copy. The bound covers a process
// outside the child's group that still holds the write end.
func (s *managedServer) waitCopied() {
	select {
	case <-s.copied:
	case <-time.After(time.Second):
	}
}

// exitError waits for the output copy, so that the last lines hold the
// child's own error message.
func (s *managedServer) exitError() error {
	s.waitCopied()
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
// kills its process group if it is still running after timeout. It returns
// once the last lines reached out.
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
	s.waitCopied()
}

// managedGroup runs managed servers that start in order and stop in reverse
// order, so that a node stops before the gateway it uplinks to.
type managedGroup struct {
	servers  []*managedServer
	logs     *logbuf.Buffer
	exited   chan error
	stopping atomic.Bool
}

// startManagedGroup starts each spec once the one before it is ready. out gets
// every line. If out is nil, the group keeps the lines for Logs instead.
func startManagedGroup(specs []spawnSpec, out io.Writer) (*managedGroup, error) {
	g := &managedGroup{exited: make(chan error, 1)}
	if out == nil {
		g.logs = logbuf.New(1000)
		out = g.logs
	}
	for _, spec := range specs {
		s, err := startManagedReady(spec, out)
		if err != nil {
			g.stop(managedStopTimeout)
			return nil, err
		}
		g.servers = append(g.servers, s)
		go g.watch(s)
	}
	return g, nil
}

func (g *managedGroup) watch(s *managedServer) {
	err := s.wait()
	if g.stopping.Load() {
		return
	}
	select {
	case g.exited <- err:
	default:
	}
}

func (g *managedGroup) Logs() *logbuf.Buffer {
	if g == nil {
		return nil
	}
	return g.logs
}

// Exited receives the first unexpected exit. A restart in place keeps the PID,
// and stop is not an exit.
func (g *managedGroup) Exited() <-chan error {
	if g == nil {
		return nil
	}
	return g.exited
}

func (g *managedGroup) stop(timeout time.Duration) {
	if g == nil {
		return
	}
	g.stopping.Store(true)
	for i := len(g.servers) - 1; i >= 0; i-- {
		g.servers[i].stop(timeout)
	}
}
