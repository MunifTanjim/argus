package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// makePeers wires two peers over an in-memory pipe. Each may register a dispatch
// and a notification sink.
func makePeers(a, b PeerOptions) (*Peer, *Peer, func()) {
	ca, cb := net.Pipe()
	pa := NewPeer(ca, a)
	pb := NewPeer(cb, b)
	return pa, pb, func() { pa.Close(); pb.Close() }
}

func echoDispatch(_ context.Context, method string, params json.RawMessage) (any, error) {
	var in struct{ Msg string }
	_ = json.Unmarshal(params, &in)
	return map[string]string{"got": in.Msg, "by": method}, nil
}

// A symmetric peer can be called by its remote end.
func TestPeerCallEitherDirection(t *testing.T) {
	// Only B serves "echo"; A calls it.
	pa, _, done := makePeers(
		PeerOptions{},
		PeerOptions{Dispatch: echoDispatch},
	)
	defer done()

	var out struct{ Got, By string }
	if err := pa.Call("echo", map[string]string{"Msg": "hi"}, &out); err != nil {
		t.Fatalf("A→B call: %v", err)
	}
	if out.Got != "hi" || out.By != "echo" {
		t.Fatalf("unexpected result: %+v", out)
	}
}

// Both ends can serve requests on the same connection (true symmetry): B calls a
// handler registered on A while A could also call B.
func TestPeerBothEndsServe(t *testing.T) {
	pa, pb, done := makePeers(
		PeerOptions{Dispatch: func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
			return map[string]int{"v": 1}, nil
		}},
		PeerOptions{Dispatch: func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
			return map[string]int{"v": 2}, nil
		}},
	)
	defer done()

	var fromB struct{ V int }
	if err := pa.Call("x", nil, &fromB); err != nil { // A→B
		t.Fatalf("A→B: %v", err)
	}
	var fromA struct{ V int }
	if err := pb.Call("y", nil, &fromA); err != nil { // B→A, same conn
		t.Fatalf("B→A: %v", err)
	}
	if fromB.V != 2 || fromA.V != 1 {
		t.Fatalf("crossed wires: fromB=%d fromA=%d", fromB.V, fromA.V)
	}
}

// Unknown methods surface as a method-not-found RPCError.
func TestPeerMethodNotFound(t *testing.T) {
	pa, _, done := makePeers(PeerOptions{}, PeerOptions{}) // B has no dispatch
	defer done()

	err := pa.Call("nope", nil, nil)
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("want method-not-found, got %v", err)
	}
}

// Notifications flow to the peer's OnNotify sink, in both directions.
func TestPeerNotifyBothDirections(t *testing.T) {
	aGot := make(chan Notification, 1)
	bGot := make(chan Notification, 1)
	pa, pb, done := makePeers(
		PeerOptions{OnNotify: func(n Notification) { aGot <- n }},
		PeerOptions{OnNotify: func(n Notification) { bGot <- n }},
	)
	defer done()

	if err := pa.Notify("ping", map[string]int{"n": 7}); err != nil {
		t.Fatalf("A notify: %v", err)
	}
	select {
	case n := <-bGot:
		if n.Method != "ping" {
			t.Fatalf("B want ping, got %q", n.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("B never received notification")
	}

	if err := pb.Notify("pong", nil); err != nil {
		t.Fatalf("B notify: %v", err)
	}
	select {
	case n := <-aGot:
		if n.Method != "pong" {
			t.Fatalf("A want pong, got %q", n.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("A never received notification")
	}
}

// A pending call returns an error when the connection closes.
func TestPeerCallUnblocksOnClose(t *testing.T) {
	block := make(chan struct{})
	pa, _, done := makePeers(
		PeerOptions{},
		PeerOptions{Dispatch: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			<-block // never returns until test ends
			return nil, nil
		}},
	)
	defer done()
	defer close(block)

	errCh := make(chan error, 1)
	go func() { errCh <- pa.Call("hang", nil, nil) }()

	time.Sleep(20 * time.Millisecond)
	pa.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("want error after close")
		}
	case <-time.After(time.Second):
		t.Fatal("Call did not unblock on close")
	}
}

// A peer whose remote end stops draining must not block a writer forever: the
// write deadline fires, the send errors, and the peer closes itself.
func TestPeerWriteDeadlineDropsStuckPeer(t *testing.T) {
	ca, cb := net.Pipe()
	defer cb.Close() // cb is never read, so any write to ca blocks until the deadline

	p := NewPeer(ca, PeerOptions{WriteTimeout: 50 * time.Millisecond})
	defer p.Close()

	errCh := make(chan error, 1)
	go func() { errCh <- p.Notify("x", map[string]int{"a": 1}) }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("want write-deadline error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Notify did not return: write deadline not applied")
	}

	select {
	case <-p.Done():
		// good: the stuck peer dropped itself
	case <-time.After(2 * time.Second):
		t.Fatal("peer not closed after write timeout")
	}
}

// With keepalive on, a remote that stops answering pings (a half-open link that
// never errors on read) is detected: the peer closes itself, firing Done.
// The remote here is a bare conn that drains and never writes — a dispatch that
// blocks would not do, since ping is answered above dispatch.
func TestPeerKeepaliveClosesOnUnresponsiveRemote(t *testing.T) {
	local, remote := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, remote) }()
	defer remote.Close()

	pa := NewPeer(local, PeerOptions{KeepaliveInterval: 20 * time.Millisecond, KeepaliveTimeout: 40 * time.Millisecond})
	defer pa.Close()

	select {
	case <-pa.Done():
		// good: keepalive detected the dead remote and closed the peer
	case <-time.After(time.Second):
		t.Fatal("keepalive did not close peer on unresponsive remote")
	}
}

// replyToPings speaks the wire protocol directly, answering every inbound request
// except the first skip of them. Ping is answered by the Peer above Dispatch, so a
// blocking dispatch can no longer simulate a missed ping — the miss has to happen
// at the transport level, as it would on a real flaky link.
func replyToPings(t *testing.T, conn net.Conn, skip int) {
	t.Helper()
	go func() {
		sc := bufio.NewScanner(conn)
		seen := 0
		for sc.Scan() {
			var m message
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
				continue
			}
			if seen++; seen <= skip {
				continue // swallow: the ping goes unanswered
			}
			b, _ := json.Marshal(message{JSONRPC: jsonrpcVersion, ID: m.ID, Result: json.RawMessage("null")})
			if _, err := conn.Write(append(b, '\n')); err != nil {
				return
			}
		}
	}()
}

// A single missed ping is a transient blip, not a death: with a failure
// threshold above one, the peer survives one timed-out ping as long as the next
// one is answered.
func TestPeerKeepaliveToleratesTransientBlip(t *testing.T) {
	local, remote := net.Pipe()
	replyToPings(t, remote, 1) // drop only the first ping
	defer remote.Close()

	pa := NewPeer(local, PeerOptions{
		KeepaliveInterval: 20 * time.Millisecond, KeepaliveTimeout: 30 * time.Millisecond, KeepaliveFailureThreshold: 2,
	})
	defer pa.Close()

	select {
	case <-pa.Done():
		t.Fatal("keepalive closed peer after a single transient blip")
	case <-time.After(200 * time.Millisecond):
		// good: one missed ping did not kill the peer
	}
}

// Keepalive leaves a healthy peer alone: as long as pings are answered, the peer
// stays open across several cycles.
func TestPeerKeepaliveStaysUpWhenAnswered(t *testing.T) {
	pa, _, done := makePeers(
		PeerOptions{KeepaliveInterval: 20 * time.Millisecond, KeepaliveTimeout: 40 * time.Millisecond},
		PeerOptions{Dispatch: func(context.Context, string, json.RawMessage) (any, error) { return nil, nil }},
	)
	defer done()

	select {
	case <-pa.Done():
		t.Fatal("keepalive closed a healthy peer")
	case <-time.After(150 * time.Millisecond):
		// good: peer survived several keepalive cycles
	}
}

// Ping is answered at the transport level before dispatch is invoked, so no
// dispatch policy can interfere with the keepalive path.
func TestPeerPingAnsweredWithoutDispatch(t *testing.T) {
	dispatched := make(chan struct{}, 1)
	_, pb, done := makePeers(
		PeerOptions{Dispatch: func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
			dispatched <- struct{}{}
			return nil, &RPCError{Code: CodeMethodNotFound, Message: "dispatch reached"}
		}},
		PeerOptions{},
	)
	defer done()

	if err := pb.Call(MethodPing, nil, nil); err != nil {
		t.Fatalf("ping to transport-aware peer: %v", err)
	}
	select {
	case <-dispatched:
		t.Fatal("ping reached dispatch; should have been answered at transport level")
	default:
		// good: ping answered before dispatch
	}
}

// An *RPCError ping reply proves the remote is alive and processing frames; it
// must not count as a keepalive miss. Only a transport failure (timeout, closed
// connection) is a real miss.
func TestPeerKeepaliveRPCErrorCountsAsAlive(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()

	// raw remote answers every ping with an RPCError (e.g. method-not-found from
	// an older peer that has no transport-level ping answer).
	go func() {
		sc := bufio.NewScanner(remote)
		for sc.Scan() {
			var m message
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
				continue
			}
			b, _ := json.Marshal(message{
				JSONRPC: jsonrpcVersion,
				ID:      m.ID,
				Error:   &RPCError{Code: CodeMethodNotFound, Message: "method not found"},
			})
			if _, err := remote.Write(append(b, '\n')); err != nil {
				return
			}
		}
	}()

	pa := NewPeer(local, PeerOptions{KeepaliveInterval: 20 * time.Millisecond, KeepaliveTimeout: 40 * time.Millisecond})
	defer pa.Close()

	select {
	case <-pa.Done():
		t.Fatal("keepalive closed peer on RPCError reply; RPCError should count as alive")
	case <-time.After(150 * time.Millisecond):
		// good: RPCError reply proves the remote responded; link is healthy
	}
}

// recordConn records every byte read from the wrapped conn.
type recordConn struct {
	net.Conn
	mu  sync.Mutex
	buf []byte
}

func (c *recordConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	c.buf = append(c.buf, p[:n]...)
	c.mu.Unlock()
	return n, err
}

func (c *recordConn) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf...)
}

func TestPeerCBORNotifyWhenRemoteAllows(t *testing.T) {
	got := make(chan Notification, 2)
	pa, _, done := makePeers(PeerOptions{}, PeerOptions{OnNotify: func(n Notification) { got <- n }})
	defer done()
	pa.SetRemoteCBOR(true)
	if !pa.RemoteCBOR() {
		t.Fatal("RemoteCBOR should be true")
	}

	if err := pa.Notify(MethodTerminalOutput, TerminalOutput{TermID: "t1", Data: []byte{0xff, 0x00}}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	n := <-got
	if n.Enc != EncodingCBOR {
		t.Fatalf("Enc = %v, want CBOR", n.Enc)
	}
	var o TerminalOutput
	if err := n.Decode(&o); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if o.TermID != "t1" || !bytes.Equal(o.Data, []byte{0xff, 0x00}) {
		t.Fatalf("decoded %+v", o)
	}

	if err := pa.Notify(MethodSessionEvent, map[string]string{"k": "v"}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	n = <-got
	if n.Enc != EncodingJSON {
		t.Fatalf("session.event Enc = %v, want JSON", n.Enc)
	}
	var m map[string]string
	if err := n.Decode(&m); err != nil || m["k"] != "v" {
		t.Fatalf("decode: %v %v", m, err)
	}
}

func TestPeerJSONWhenRemoteNotAllowed(t *testing.T) {
	ca, cb := net.Pipe()
	rb := &recordConn{Conn: cb}
	got := make(chan Notification, 1)
	pa := NewPeer(ca, PeerOptions{})
	pb := NewPeer(rb, PeerOptions{OnNotify: func(n Notification) { got <- n }})
	defer pa.Close()
	defer pb.Close()

	if err := pa.Notify(MethodTerminalOutput, TerminalOutput{TermID: "t1", Data: []byte("x")}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	n := <-got
	if n.Enc != EncodingJSON {
		t.Fatalf("Enc = %v, want JSON", n.Enc)
	}
	if raw := rb.bytes(); len(raw) == 0 || raw[0] != '{' {
		t.Fatalf("wire bytes = %q, want a JSON line", raw)
	}
}

func TestPeerCBORRequestTranscodedAndAnsweredInCBOR(t *testing.T) {
	ca, cb := net.Pipe()
	ra := &recordConn{Conn: ca}
	seen := make(chan json.RawMessage, 1)
	pa := NewPeer(ra, PeerOptions{})
	pb := NewPeer(cb, PeerOptions{Dispatch: func(_ context.Context, _ string, params json.RawMessage) (any, error) {
		seen <- params
		return nil, nil
	}})
	defer pa.Close()
	defer pb.Close()
	pa.SetRemoteCBOR(true)

	if err := pa.Call(MethodTerminalInput, TerminalInputParams{TermID: "t1", Data: []byte("x")}, nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got, want := string(<-seen), `{"data":"eA==","term_id":"t1"}`; got != want {
		t.Fatalf("dispatch params = %s, want %s", got, want)
	}
	if raw := ra.bytes(); len(raw) == 0 || raw[0] != cborMarker {
		t.Fatalf("response wire bytes = %q, want a CBOR frame", raw)
	}
}

func TestPeerCBORParseError(t *testing.T) {
	ca, cb := net.Pipe()
	pb := NewPeer(cb, PeerOptions{Dispatch: echoDispatch})
	defer pb.Close()
	defer ca.Close()
	r := bufio.NewReader(ca)

	go func() { _, _ = ca.Write(appendFrame(nil, EncodingCBOR, []byte{0xff})) }()
	enc, line, err := readFrame(r)
	if err != nil || enc != EncodingJSON {
		t.Fatalf("read reply: enc=%v err=%v", enc, err)
	}
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.ID != nil || m.Error == nil || m.Error.Code != CodeParseError || m.Error.Message != "parse error" {
		t.Fatalf("reply = %s", line)
	}

	go func() {
		_, _ = ca.Write([]byte(`{"jsonrpc":"2.0","id":7,"method":"echo","params":{"Msg":"hi"}}` + "\n"))
	}()
	_, line, err = readFrame(r)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	m = message{}
	if err := json.Unmarshal(line, &m); err != nil || m.ID == nil || string(*m.ID) != "7" || m.Error != nil {
		t.Fatalf("follow-up reply = %s (%v)", line, err)
	}
}

func TestPeerRelayFrameKeepsEncoding(t *testing.T) {
	ca, cb := net.Pipe()
	got := make(chan RelayFrame, 1)
	pb := NewPeer(cb, PeerOptions{OnRelayFrame: func(_ *Peer, f RelayFrame) { got <- f }})
	defer pb.Close()
	defer ca.Close()

	envelope, err := CBORMarshal(cborMessage{
		JSONRPC: jsonrpcVersion,
		Method:  MethodTerminalOutput,
		Route:   &RouteHeader{ChanID: "c1"},
		Body:    []byte{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	go func() { _, _ = ca.Write(appendFrame(nil, EncodingCBOR, envelope)) }()
	f := <-got
	if f.Enc != EncodingCBOR || f.Route.ChanID != "c1" || f.Method != MethodTerminalOutput {
		t.Fatalf("frame = %+v", f)
	}
	if !bytes.Equal(f.Body, []byte{1, 2, 3}) || !bytes.Equal(f.Raw, envelope) {
		t.Fatalf("Body = %x Raw = %x", f.Body, f.Raw)
	}
}

// frameRecorder implements FrameWriter and records each complete frame.
type frameRecorder struct {
	net.Conn
	frames chan []byte
}

func (c *frameRecorder) WriteFrame(_ Encoding, frame []byte) error {
	c.frames <- append([]byte(nil), frame...)
	_, err := c.Conn.Write(frame)
	return err
}

func TestPeerUsesFrameWriter(t *testing.T) {
	ca, cb := net.Pipe()
	fr := &frameRecorder{Conn: ca, frames: make(chan []byte, 1)}
	got := make(chan Notification, 1)
	pa := NewPeer(fr, PeerOptions{})
	pb := NewPeer(cb, PeerOptions{OnNotify: func(n Notification) { got <- n }})
	defer pa.Close()
	defer pb.Close()
	pa.SetRemoteCBOR(true)

	if err := pa.Notify(MethodTerminalOutput, TerminalOutput{TermID: "t1", Data: []byte("x")}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	frame := <-fr.frames
	<-got
	enc, payload, err := readFrame(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil || enc != EncodingCBOR || len(payload)+5 != len(frame) {
		t.Fatalf("frame not a single complete CBOR frame: enc=%v err=%v", enc, err)
	}
}
