package pi

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type hostHarness struct {
	t      *testing.T
	sock   string
	inR    *bufio.Reader
	outW   *io.PipeWriter
	killed chan struct{}
	exited chan hostExit
	done   chan struct{}
}

func newHostHarness(t *testing.T) *hostHarness {
	t.Helper()
	oldWait := hostFirstClientWait
	hostFirstClientWait = 100 * time.Millisecond
	t.Cleanup(func() { hostFirstClientWait = oldWait })
	dir, err := os.MkdirTemp("", "aph")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &hostHarness{
		t:      t,
		sock:   sock,
		inR:    bufio.NewReader(inR),
		outW:   outW,
		killed: make(chan struct{}, 1),
		exited: make(chan hostExit, 1),
		done:   make(chan struct{}),
	}
	kill := func() {
		select {
		case h.killed <- struct{}{}:
		default:
		}
	}
	go func() {
		defer close(h.done)
		serveHost(ln, "/w", inW, outR, kill, h.exited)
	}()
	t.Cleanup(func() {
		select {
		case h.exited <- hostExit{}:
		default:
		}
		outW.Close()
		inR.Close()
		<-h.done
	})
	return h
}

func (h *hostHarness) piWrite(line string) {
	h.t.Helper()
	if _, err := h.outW.Write([]byte(line + "\n")); err != nil {
		h.t.Fatal(err)
	}
}

func (h *hostHarness) piRead() string {
	h.t.Helper()
	ch := make(chan string, 1)
	go func() {
		l, _ := h.inR.ReadString('\n')
		ch <- l
	}()
	select {
	case l := <-ch:
		return strings.TrimSuffix(l, "\n")
	case <-time.After(2 * time.Second):
		h.t.Fatal("timed out reading pi stdin")
		return ""
	}
}

type hostClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func (h *hostHarness) dial() *hostClient {
	h.t.Helper()
	conn, err := net.Dial("unix", h.sock)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { conn.Close() })
	return &hostClient{t: h.t, conn: conn, r: bufio.NewReader(conn)}
}

func (c *hostClient) read() string {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	l, err := c.r.ReadString('\n')
	if err != nil {
		c.t.Fatalf("client read: %v", err)
	}
	return strings.TrimSuffix(l, "\n")
}

func (c *hostClient) readType() string {
	c.t.Helper()
	var rec struct {
		Type string `json:"type"`
	}
	l := c.read()
	if err := json.Unmarshal([]byte(l), &rec); err != nil {
		c.t.Fatalf("decode %q: %v", l, err)
	}
	return rec.Type
}

func (c *hostClient) write(line string) {
	c.t.Helper()
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		c.t.Fatal(err)
	}
}

// connect dials and consumes the hello.
func (h *hostHarness) connect() *hostClient {
	h.t.Helper()
	c := h.dial()
	if typ := c.readType(); typ != hostHelloType {
		h.t.Fatalf("first record type = %q, want %q", typ, hostHelloType)
	}
	return c
}

func TestHostSendsHelloWithCwd(t *testing.T) {
	h := newHostHarness(t)
	c := h.dial()
	var hello struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(c.read()), &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Type != hostHelloType || hello.Cwd != "/w" {
		t.Fatalf("hello = %+v", hello)
	}
}

func TestHostRelaysBothWays(t *testing.T) {
	h := newHostHarness(t)
	c := h.connect()
	c.write(`{"type":"get_state","id":"1"}`)
	if got := h.piRead(); got != `{"type":"get_state","id":"1"}` {
		t.Fatalf("pi got %q", got)
	}
	h.piWrite("{\"type\":\"agent_start\",\"text\":\"a b\"}")
	if got := c.read(); got != "{\"type\":\"agent_start\",\"text\":\"a b\"}" {
		t.Fatalf("client got %q", got)
	}
}

func TestHostKeepsPiAfterClientLeaves(t *testing.T) {
	h := newHostHarness(t)
	c := h.connect()
	c.conn.Close()
	time.Sleep(50 * time.Millisecond)
	c2 := h.connect()
	c2.write(`{"type":"abort"}`)
	if got := h.piRead(); got != `{"type":"abort"}` {
		t.Fatalf("pi got %q", got)
	}
	h.piWrite(`{"type":"agent_end"}`)
	if got := c2.read(); got != `{"type":"agent_end"}` {
		t.Fatalf("client got %q", got)
	}
}

func TestHostReplaysOpenDialogs(t *testing.T) {
	h := newHostHarness(t)
	u1 := `{"type":"extension_ui_request","id":"u1","method":"confirm","title":"a"}`
	u2 := `{"type":"extension_ui_request","id":"u2","method":"select","title":"b","options":["x"]}`
	h.piWrite(u1)
	h.piWrite(`{"type":"extension_ui_request","id":"n1","method":"notify","message":"hi"}`)
	h.piWrite(u2)
	c := h.connect()
	if got := c.read(); got != u1 {
		t.Fatalf("replay[0] = %q", got)
	}
	if got := c.read(); got != u2 {
		t.Fatalf("replay[1] = %q", got)
	}
	resp := `{"type":"extension_ui_response","id":"u1","confirmed":true}`
	c.write(resp)
	if got := h.piRead(); got != resp {
		t.Fatalf("pi got %q", got)
	}
	c.conn.Close()
	c2 := h.connect()
	if got := c2.read(); got != u2 {
		t.Fatalf("replay after answer = %q", got)
	}
	h.piWrite(`{"type":"agent_end"}`)
	if got := c2.read(); got != `{"type":"agent_end"}` {
		t.Fatalf("live line = %q", got)
	}
}

func TestHostDropsATimedOutDialog(t *testing.T) {
	h := newHostHarness(t)
	h.piWrite(`{"type":"extension_ui_request","id":"u1","method":"select","title":"a","options":["x"],"timeout":50}`)
	time.Sleep(100 * time.Millisecond)
	c := h.connect()
	h.piWrite(`{"type":"agent_end"}`)
	if got := c.read(); got != `{"type":"agent_end"}` {
		t.Fatalf("first after hello = %q", got)
	}
}

func TestHostDropsTheDialogsOfASettledRun(t *testing.T) {
	h := newHostHarness(t)
	idle := `{"type":"extension_ui_request","id":"u1","method":"confirm","title":"a"}`
	h.piWrite(idle)
	h.piWrite(`{"type":"agent_start"}`)
	h.piWrite(`{"type":"extension_ui_request","id":"u2","method":"confirm","title":"b"}`)
	h.piWrite(`{"type":"agent_settled","aborted":true}`)
	c := h.connect()
	if got := c.read(); got != idle {
		t.Fatalf("replay[0] = %q", got)
	}
	h.piWrite(`{"type":"agent_end"}`)
	if got := c.read(); got != `{"type":"agent_end"}` {
		t.Fatalf("after replay = %q, want the run's dialog dropped", got)
	}
}

func TestHostReplaysTheRemainingTimeout(t *testing.T) {
	h := newHostHarness(t)
	h.piWrite(`{"type":"extension_ui_request","id":"u1","method":"select","title":"a","options":["x"],"timeout":5000}`)
	time.Sleep(300 * time.Millisecond)
	c := h.connect()
	var rec struct {
		ID      string   `json:"id"`
		Method  string   `json:"method"`
		Title   string   `json:"title"`
		Options []string `json:"options"`
		Timeout int      `json:"timeout"`
	}
	line := c.read()
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.ID != "u1" || rec.Method != "select" || rec.Title != "a" || len(rec.Options) != 1 {
		t.Fatalf("replayed %q", line)
	}
	if rec.Timeout < 1 || rec.Timeout > 4700 {
		t.Fatalf("replayed timeout = %d, want what is left of 5000", rec.Timeout)
	}
}

func TestHostReplaysTheLastQueue(t *testing.T) {
	h := newHostHarness(t)
	h.piWrite(`{"type":"queue_update","steering":["one"],"followUp":[]}`)
	q2 := `{"type":"queue_update","steering":["two"],"followUp":[]}`
	h.piWrite(q2)
	c := h.connect()
	if got := c.read(); got != q2 {
		t.Fatalf("replay = %q", got)
	}
	h.piWrite(`{"type":"agent_end"}`)
	if got := c.read(); got != `{"type":"agent_end"}` {
		t.Fatalf("live line = %q", got)
	}
}

func TestHostDrainsWithoutAClient(t *testing.T) {
	h := newHostHarness(t)
	buf := []byte(strings.Repeat("x", 1<<20))
	done := make(chan error, 1)
	go func() {
		_, err := h.outW.Write(buf)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pi stdout write blocked without a client")
	}
}

func TestHostNewClientReplacesTheOld(t *testing.T) {
	h := newHostHarness(t)
	c1 := h.connect()
	c2 := h.connect()
	c1.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c1.r.ReadString('\n'); !errors.Is(err, io.EOF) {
		t.Fatalf("old client read err = %v, want EOF", err)
	}
	h.piWrite(`{"type":"agent_end"}`)
	if got := c2.read(); got != `{"type":"agent_end"}` {
		t.Fatalf("new client got %q", got)
	}
}

func TestHostStopClosesPiStdin(t *testing.T) {
	h := newHostHarness(t)
	c := h.connect()
	c.write(`{"type":"` + hostStopType + `"}`)
	ch := make(chan error, 1)
	go func() {
		l, err := h.inR.ReadString('\n')
		if l != "" {
			err = errors.New("forwarded: " + l)
		}
		ch <- err
	}()
	select {
	case err := <-ch:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("pi stdin err = %v, want EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pi stdin not closed")
	}
}

func TestHostStopKillsAStuckPi(t *testing.T) {
	old := hostKillAfter
	hostKillAfter = 50 * time.Millisecond
	t.Cleanup(func() { hostKillAfter = old })
	h := newHostHarness(t)
	c := h.connect()
	c.write(`{"type":"` + hostStopType + `"}`)
	select {
	case <-h.killed:
	case <-time.After(2 * time.Second):
		t.Fatal("kill not called")
	}
}

func TestHostReportsTheExit(t *testing.T) {
	h := newHostHarness(t)
	c := h.connect()
	h.outW.Close()
	h.exited <- hostExit{Code: 3, Stderr: "boom"}
	var rec struct {
		Type   string `json:"type"`
		Code   int    `json:"code"`
		Stderr string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(c.read()), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Type != hostExitType || rec.Code != 3 || rec.Stderr != "boom" {
		t.Fatalf("exit record = %+v", rec)
	}
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveHost did not return")
	}
	if _, err := os.Stat(h.sock); !os.IsNotExist(err) {
		t.Fatalf("socket file still present: %v", err)
	}
}

func TestHostHoldsTheExitForTheFirstClient(t *testing.T) {
	h := newHostHarness(t)
	hostFirstClientWait = 2 * time.Second
	h.outW.Close()
	h.exited <- hostExit{Code: 1, Stderr: "bad extension"}
	time.Sleep(100 * time.Millisecond)
	c := h.connect()
	var rec struct {
		Type   string `json:"type"`
		Stderr string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(c.read()), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Type != hostExitType || rec.Stderr != "bad extension" {
		t.Fatalf("exit record = %+v", rec)
	}
}

func TestHostSocketPathFitsSunPath(t *testing.T) {
	p := hostSocketPath(strings.Repeat("a", 32))
	t.Logf("socket path %q (%d bytes)", p, len(p))
	if len(p) >= 104 {
		t.Fatalf("socket path is %d bytes, sun_path limit is 104", len(p))
	}
	if filepath.Base(p) != strings.Repeat("a", 16)+".sock" {
		t.Fatalf("socket name = %q", filepath.Base(p))
	}
}

func TestRunHostRunsTheCommand(t *testing.T) {
	dir, err := os.MkdirTemp("", "aph")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "sub", "r.sock")
	code := make(chan int, 1)
	go func() {
		code <- RunHost(sock, "/spawn/dir", []string{"sh", "-c", `read l; echo "$l"; echo oops >&2; exit 4`})
	}()

	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer conn.Close()
	c := &hostClient{t: t, conn: conn, r: bufio.NewReader(conn)}
	var hello struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(c.read()), &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Type != hostHelloType || hello.Cwd != "/spawn/dir" {
		t.Fatalf("hello = %+v", hello)
	}
	c.write("hello")
	if got := c.read(); got != "hello" {
		t.Fatalf("relayed %q", got)
	}
	var rec struct {
		Type   string `json:"type"`
		Code   int    `json:"code"`
		Stderr string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(c.read()), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Type != hostExitType || rec.Code != 4 || rec.Stderr != "oops" {
		t.Fatalf("exit record = %+v", rec)
	}
	select {
	case got := <-code:
		if got != 4 {
			t.Fatalf("RunHost = %d, want 4", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunHost did not return")
	}
	if fi, err := os.Stat(filepath.Dir(sock)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket dir: %v %v", fi, err)
	}
}

func TestRunHostSocketIsOwnerOnly(t *testing.T) {
	dir, err := os.MkdirTemp("", "aph")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(sub, "r.sock")
	code := make(chan int, 1)
	go func() { code <- RunHost(sock, "", []string{"sh", "-c", "read l"}) }()

	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer conn.Close()
	fi, err := os.Stat(sock)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode: %v %v", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(sub); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("socket dir mode: %v %v", fi.Mode().Perm(), err)
	}
	if _, err := conn.Write([]byte("bye\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-code:
	case <-time.After(5 * time.Second):
		t.Fatal("RunHost did not return")
	}
}
