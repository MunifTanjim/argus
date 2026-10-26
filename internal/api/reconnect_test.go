package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// flapSrv is a minimal server that can be stopped and restarted on the same unix
// socket, to exercise the client's reconnect behavior. Each accepted connection greets
// the client with a "hello" notification and answers "echo".
type flapSrv struct {
	socket string
	mu     sync.Mutex
	l      net.Listener
	peers  []*Peer
}

func newFlapSrv(t *testing.T) *flapSrv {
	t.Helper()
	s := &flapSrv{socket: filepath.Join(t.TempDir(), "r.sock")}
	s.start(t)
	t.Cleanup(s.stop)
	return s
}

func (s *flapSrv) start(t *testing.T) {
	t.Helper()
	l, err := net.Listen("unix", s.socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.mu.Lock()
	s.l = l
	s.mu.Unlock()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			p := NewPeer(conn, PeerOptions{
				Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
					if method == "echo" {
						return "pong", nil
					}
					return nil, &RPCError{Code: CodeMethodNotFound, Message: method}
				},
			})
			_ = p.Notify("hello", nil)
			s.mu.Lock()
			s.peers = append(s.peers, p)
			s.mu.Unlock()
		}
	}()
}

func (s *flapSrv) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l != nil {
		s.l.Close() // Go unlinks the unix socket file on close, freeing it for restart
		s.l = nil
	}
	for _, p := range s.peers {
		p.Close()
	}
	s.peers = nil
}

func recvState(t *testing.T, c *ReconnectingClient) bool {
	t.Helper()
	select {
	case s := <-c.States():
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a connection-state transition")
		return false
	}
}

func waitNotify(t *testing.T, c *ReconnectingClient, method string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case n := <-c.Events():
			if n.Method == method {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q notification", method)
		}
	}
}

func TestReconnectingClientRecovers(t *testing.T) {
	srv := newFlapSrv(t)
	dial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", srv.socket)
	}

	c, err := NewReconnectingClient(context.Background(), dial)
	if err != nil {
		t.Fatalf("NewReconnectingClient: %v", err)
	}
	defer c.Close()

	waitNotify(t, c, "hello") // greeting from the first connection
	var out string
	if err := c.Call("echo", nil, &out); err != nil || out != "pong" {
		t.Fatalf("echo before drop = (%q, %v)", out, err)
	}

	// Drop the server: the client should report disconnected and fail calls.
	srv.stop()
	if recvState(t, c) {
		t.Fatal("expected disconnected (false) state after server stop")
	}
	if err := c.Call("echo", nil, &out); err == nil {
		t.Fatal("expected Call to error while disconnected")
	}

	// Bring the server back and kick an immediate reconnect.
	srv.start(t)
	c.Reconnect()
	if !recvState(t, c) {
		t.Fatal("expected reconnected (true) state after server restart")
	}
	waitNotify(t, c, "hello") // greeting from the new connection
	if err := c.Call("echo", nil, &out); err != nil || out != "pong" {
		t.Fatalf("echo after reconnect = (%q, %v)", out, err)
	}
}

func TestReconnectingClientFirstDialError(t *testing.T) {
	dial := func(context.Context) (net.Conn, error) { return nil, errors.New("nope") }
	if _, err := NewReconnectingClient(context.Background(), dial); err == nil {
		t.Fatal("expected first-dial failure to be returned")
	}
}

type served struct {
	method string
	enc    Encoding
}

// serveNodeFrames answers node.identify (declaring cbor when asked) and
// terminal.input on conn, reporting each request's method and encoding.
func serveNodeFrames(conn net.Conn, cbor bool, reqs chan<- served) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		enc, payload, err := readFrame(r)
		if err != nil {
			return
		}
		m, err := unmarshalMessage(enc, payload)
		if err != nil || m.ID == nil {
			continue
		}
		reqs <- served{m.Method, enc}
		resp := message{enc: enc, ID: m.ID}
		switch m.Method {
		case MethodNodeIdentify:
			resp.Result, _ = enc.Marshal(IdentifyResult{Capabilities: NodeCapabilities{CBOR: cbor}})
		case MethodTerminalInput:
			resp.Result, _ = enc.Marshal(nil)
		default:
			resp.Error = &RPCError{Code: CodeMethodNotFound, Message: m.Method}
		}
		b, _ := resp.marshal()
		if _, err := conn.Write(appendFrame(nil, enc, b)); err != nil {
			return
		}
	}
}

// awaitIdentify waits for the identify request and then for RemoteCBOR to
// become want.
func awaitIdentify(t *testing.T, c *ReconnectingClient, reqs <-chan served, want bool) {
	t.Helper()
	select {
	case r := <-reqs:
		if r.method != MethodNodeIdentify {
			t.Fatalf("first request = %s, want node.identify", r.method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no node.identify")
	}
	if !want {
		time.Sleep(50 * time.Millisecond) // let the identify reply land
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for p := c.currentPeer(); p == nil || !p.RemoteCBOR(); p = c.currentPeer() {
		if time.Now().After(deadline) {
			t.Fatal("RemoteCBOR never became true")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func sendInput(t *testing.T, call func(string, any, any) error, reqs <-chan served) Encoding {
	t.Helper()
	if err := call(MethodTerminalInput, TerminalInputParams{TermID: "t1", Data: []byte{0xff}}, nil); err != nil {
		t.Fatalf("terminal.input: %v", err)
	}
	r := <-reqs
	if r.method != MethodTerminalInput {
		t.Fatalf("request = %s, want terminal.input", r.method)
	}
	return r.enc
}

func TestUnixClientNegotiatesCBOR(t *testing.T) {
	newClient := func(t *testing.T, cbor ...bool) (*ReconnectingClient, chan served, func(int)) {
		reqs := make(chan served, 8)
		var mu sync.Mutex
		var servers []net.Conn
		dial := func(context.Context) (net.Conn, error) {
			srv, cli := net.Pipe()
			mu.Lock()
			on := cbor[min(len(servers), len(cbor)-1)]
			servers = append(servers, srv)
			mu.Unlock()
			go serveNodeFrames(srv, on, reqs)
			return cli, nil
		}
		c, err := NewReconnectingClient(context.Background(), dial)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		drop := func(i int) {
			mu.Lock()
			defer mu.Unlock()
			servers[i].Close()
		}
		return c, reqs, drop
	}
	t.Run("new node", func(t *testing.T) {
		c, reqs, _ := newClient(t, true)
		awaitIdentify(t, c, reqs, true)
		if enc := sendInput(t, c.Call, reqs); enc != EncodingCBOR {
			t.Fatalf("terminal.input encoding = %v, want CBOR", enc)
		}
	})
	t.Run("old node", func(t *testing.T) {
		c, reqs, _ := newClient(t, false)
		awaitIdentify(t, c, reqs, false)
		if enc := sendInput(t, c.Call, reqs); enc != EncodingJSON {
			t.Fatalf("terminal.input encoding = %v, want JSON", enc)
		}
	})
	t.Run("reconnect negotiates again", func(t *testing.T) {
		c, reqs, drop := newClient(t, false, true)
		awaitIdentify(t, c, reqs, false)
		if enc := sendInput(t, c.Call, reqs); enc != EncodingJSON {
			t.Fatalf("terminal.input to old node = %v, want JSON", enc)
		}
		drop(0)
		if recvState(t, c) || !recvState(t, c) {
			t.Fatal("want a disconnect then a reconnect")
		}
		awaitIdentify(t, c, reqs, true)
		if enc := sendInput(t, c.Call, reqs); enc != EncodingCBOR {
			t.Fatalf("terminal.input after reconnect = %v, want CBOR", enc)
		}
	})
}

func TestPlainClientSendsNoIdentify(t *testing.T) {
	srv, cli := net.Pipe()
	reqs := make(chan served, 8)
	go serveNodeFrames(srv, true, reqs)
	c := NewClient(cli)
	defer c.Close()
	if enc := sendInput(t, c.Call, reqs); enc != EncodingJSON {
		t.Fatalf("terminal.input encoding = %v, want JSON", enc)
	}
	select {
	case r := <-reqs:
		t.Fatalf("unexpected %s request", r.method)
	case <-time.After(50 * time.Millisecond):
	}
}
