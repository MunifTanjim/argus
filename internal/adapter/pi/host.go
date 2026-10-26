package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
)

const (
	hostHelloType = "argus_host_hello"
	hostStopType  = "argus_host_stop"
	hostExitType  = "argus_host_exit"
)

var hostKillAfter = 5 * time.Second

// hostFirstClientWait bounds how long a relay whose pi exited holds the exit
// record for a client that has not connected yet.
var hostFirstClientWait = 5 * time.Second

var hostDir = func() string { return config.GetRuntimePath("pi") }

func hostSocketPath(sessionID string) string {
	return filepath.Join(hostDir(), sessionID[:min(16, len(sessionID))]+".sock")
}

type hostExit struct {
	Code   int
	Stderr string
}

type hostDialog struct {
	id      string
	line    []byte
	expires time.Time
	inRun   bool
}

type host struct {
	cwd       string
	piIn      io.WriteCloser
	kill      func()
	killAfter time.Duration
	exited    chan struct{}

	wmu         sync.Mutex
	stopOnce    sync.Once
	connected   chan struct{}
	connectOnce sync.Once

	mu      sync.Mutex
	client  net.Conn
	closed  bool
	running bool
	dialogs []hostDialog
	queue   []byte
}

// serveHost relays pi's stdio to one client at a time on ln until pi exits.
func serveHost(ln net.Listener, cwd string, piIn io.WriteCloser, piOut io.Reader, kill func(), exited <-chan hostExit) {
	h := &host{cwd: cwd, piIn: piIn, kill: kill, killAfter: hostKillAfter, exited: make(chan struct{}), connected: make(chan struct{})}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		h.readPi(piOut)
	}()
	go h.accept(ln)

	ex := <-exited
	close(h.exited)
	// pi's last lines can still be in the pipe when its exit status arrives.
	select {
	case <-readDone:
	case <-time.After(time.Second):
	}
	// A pi that exits at start can beat the spawner's first dial, and the
	// exit record is the only way its reason reaches argusd.
	select {
	case <-h.connected:
	case <-time.After(hostFirstClientWait):
	}
	h.mu.Lock()
	h.closed = true
	if h.client != nil {
		rec, _ := json.Marshal(map[string]any{"type": hostExitType, "code": ex.Code, "stderr": ex.Stderr})
		h.client.Write(append(rec, '\n'))
		h.client.Close()
		h.client = nil
	}
	h.mu.Unlock()
	path := ln.Addr().String()
	ln.Close()
	os.Remove(path)
}

func (h *host) readPi(piOut io.Reader) {
	r := bufio.NewReader(piOut)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		h.mu.Lock()
		h.record(line)
		if h.client != nil {
			if _, err := h.client.Write(line); err != nil {
				h.client.Close()
				h.client = nil
			}
		}
		h.mu.Unlock()
	}
}

func (h *host) record(line []byte) {
	if streamingDelta(line) {
		return
	}
	var rec struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Timeout int    `json:"timeout"`
	}
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	switch rec.Type {
	case "queue_update":
		h.queue = line
	case "agent_start":
		h.running = true
	case "agent_settled":
		h.running = false
		// pi emits nothing for a dialog its caller aborts, but a run cannot
		// settle while one of its dialogs is open.
		h.dialogs = slices.DeleteFunc(h.dialogs, func(d hostDialog) bool { return d.inRun })
	case "extension_ui_request":
		switch rec.Method {
		case "select", "confirm", "input", "editor":
		default:
			return
		}
		d := hostDialog{id: rec.ID, line: line, inRun: h.running}
		if rec.Timeout > 0 {
			d.expires = time.Now().Add(time.Duration(rec.Timeout) * time.Millisecond)
		}
		h.dialogs = append(h.dialogs, d)
	}
}

func (h *host) accept(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			c.Close()
			return
		}
		if h.client != nil {
			h.client.Close()
		}
		h.client = c
		if err := h.replay(c); err != nil {
			c.Close()
			h.client = nil
			h.mu.Unlock()
			continue
		}
		h.connectOnce.Do(func() { close(h.connected) })
		h.mu.Unlock()
		go h.readClient(c)
	}
}

// replay brings a new client to pi's current state: it missed every line
// while no client was connected, and only the open dialogs and the last queue
// still matter.
func (h *host) replay(c net.Conn) error {
	hello, _ := json.Marshal(map[string]any{"type": hostHelloType, "cwd": h.cwd})
	buf := append(hello, '\n')
	now := time.Now()
	open := h.dialogs[:0]
	for _, d := range h.dialogs {
		if !d.expires.IsZero() && now.After(d.expires) {
			continue
		}
		open = append(open, d)
		buf = append(buf, withRemainingTimeout(d, now)...)
	}
	h.dialogs = open
	buf = append(buf, h.queue...)
	_, err := c.Write(buf)
	return err
}

// withRemainingTimeout is d's request with its timeout cut to what is left,
// so pi's own timeout and the replayed one end together.
func withRemainingTimeout(d hostDialog, now time.Time) []byte {
	if d.expires.IsZero() {
		return d.line
	}
	var rec map[string]json.RawMessage
	if json.Unmarshal(d.line, &rec) != nil {
		return d.line
	}
	rec["timeout"], _ = json.Marshal(max(1, d.expires.Sub(now).Milliseconds()))
	line, err := json.Marshal(rec)
	if err != nil {
		return d.line
	}
	return append(line, '\n')
}

func (h *host) readClient(c net.Conn) {
	defer func() {
		h.mu.Lock()
		if h.client == c {
			h.client = nil
		}
		h.mu.Unlock()
		c.Close()
	}()
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var rec struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		_ = json.Unmarshal(line, &rec)
		switch rec.Type {
		case hostStopType:
			h.stop()
			continue
		case "extension_ui_response":
			h.mu.Lock()
			for i, d := range h.dialogs {
				if d.id == rec.ID {
					h.dialogs = append(h.dialogs[:i], h.dialogs[i+1:]...)
					break
				}
			}
			h.mu.Unlock()
		}
		h.wmu.Lock()
		h.piIn.Write(line)
		h.wmu.Unlock()
	}
}

// stop closes pi's stdin, which pi treats as a request to exit; a pi stuck in
// a tool call never reads it, so it is killed after hostKillAfter. The close
// skips wmu: a forward blocked on a full stdin pipe holds it.
func (h *host) stop() {
	h.stopOnce.Do(func() {
		go func() {
			t := time.NewTimer(h.killAfter)
			defer t.Stop()
			select {
			case <-t.C:
				h.kill()
			case <-h.exited:
			}
		}()
		h.piIn.Close()
	})
}

// RunHost listens on socketPath, starts argv, and relays its stdio until it
// exits. Its hello reports cwd, or the working directory when cwd is empty. It
// returns argv's exit code.
func RunHost(socketPath, cwd string, argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "pi-host: no command")
		return 2
	}
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "pi-host: %v\n", err)
		return 1
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "pi-host: %v\n", err)
		return 1
	}
	ln, err := api.ListenOwnerOnly(socketPath, slog.Default())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-host: %v\n", err)
		return 1
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		ln.Close()
		fmt.Fprintf(os.Stderr, "pi-host: %v\n", err)
		return 1
	}
	// An *os.File stdout keeps Wait from closing the read end before the
	// relay has read pi's last lines.
	outR, outW, err := os.Pipe()
	if err != nil {
		ln.Close()
		fmt.Fprintf(os.Stderr, "pi-host: %v\n", err)
		return 1
	}
	cmd.Stdout = outW
	tail := &stderrTail{}
	cmd.Stderr = tail
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		ln.Close()
		outR.Close()
		outW.Close()
		fmt.Fprintf(os.Stderr, "pi-host: start: %v\n", err)
		return 1
	}
	outW.Close()
	defer outR.Close()

	exited := make(chan hostExit, 1)
	go func() {
		cmd.Wait()
		exited <- hostExit{Code: cmd.ProcessState.ExitCode(), Stderr: tail.lastLines(3)}
	}()
	kill := func() { cmd.Process.Kill() }
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	serveHost(ln, cwd, stdin, outR, kill, exited)
	return cmd.ProcessState.ExitCode()
}
