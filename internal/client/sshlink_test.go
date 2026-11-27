package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/sshconn"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// startSocketNode runs a real node serving its local socket. A nil key makes a
// plaintext node.
func startSocketNode(t *testing.T, ctx context.Context, id, label string, key *e2e.KeyPair) (*node.Node, string) {
	t.Helper()
	n := node.New()
	n.SetIdentity(id, label)
	n.SetVersion("itest")
	if key != nil {
		n.SetIdentityKey(*key)
		n.SetE2EE(true)
	}
	dir, err := os.MkdirTemp("", "ss")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "n.sock")
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = n.Run(ctx, sock)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = os.RemoveAll(dir)
	})
	waitUntil(t, "node socket", func() bool {
		c, err := net.Dial("unix", sock)
		if err == nil {
			c.Close()
		}
		return err == nil
	})
	return n, sock
}

type sshDialCall struct {
	dest, port string
	cmd        []string
}

// fakeSSH stands in for the ssh process: each dial gets a net.Pipe whose far
// end is copied to and from a real node's local socket, as `argus bridge`
// would.
type fakeSSH struct {
	mu        sync.Mutex
	sockets   map[string]string // ssh destination -> node socket
	calls     []sshDialCall
	remotes   []io.Closer
	blackhole atomic.Bool // drop bytes the client writes
}

func installFakeSSH(t *testing.T, sockets map[string]string) *fakeSSH {
	t.Helper()
	f := &fakeSSH{sockets: sockets}
	prev := sshDial
	sshDial = f.dial
	t.Cleanup(func() {
		sshDial = prev
		f.drop()
	})
	return f
}

func (f *fakeSSH) dial(dest, port string, cmd []string, _ *slog.Logger) (net.Conn, error) {
	f.mu.Lock()
	f.calls = append(f.calls, sshDialCall{dest: dest, port: port, cmd: cmd})
	sock, ok := f.sockets[dest]
	f.mu.Unlock()
	if !ok {
		return nil, &net.OpError{Op: "ssh", Err: os.ErrNotExist}
	}
	uc, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	local, remote := net.Pipe()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := remote.Read(buf)
			if n > 0 && !f.blackhole.Load() {
				if _, werr := uc.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		uc.Close()
		remote.Close()
	}()
	go func() {
		_, _ = io.Copy(remote, uc)
		uc.Close()
		remote.Close()
	}()
	f.mu.Lock()
	f.remotes = append(f.remotes, remote, uc)
	f.mu.Unlock()
	return local, nil
}

// drop kills every link, as a dying ssh process would.
func (f *fakeSSH) drop() {
	f.mu.Lock()
	remotes := f.remotes
	f.remotes = nil
	f.mu.Unlock()
	for _, c := range remotes {
		c.Close()
	}
}

func (f *fakeSSH) dials() []sshDialCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sshDialCall(nil), f.calls...)
}

type sshFleet struct {
	c    *E2EClient
	n    *node.Node
	sock string
	gw   string
	ctx  context.Context
}

// startSSHFleet runs a real gateway and a real node uplinked to it, and
// connects a client that has the node open on the relay.
func startSSHFleet(t *testing.T, id, label string, plaintext bool) sshFleet {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ts := httptest.NewServer(gateway.NewServer(gateway.New(time.Second), nil, nil).Handler())
	t.Cleanup(ts.Close)
	gw := "ws://" + strings.TrimPrefix(ts.URL, "http://")

	var key *e2e.KeyPair
	if !plaintext {
		kp := mustKP(t)
		key = &kp
	}
	n, sock := startSocketNode(t, ctx, id, label, key)
	n.Registry().ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%10",
		AgentSessionID: id + "-s1", Status: session.StatusIdle,
	})
	go n.ConnectGateway(ctx, gw+"/node", "", nil)
	waitUntil(t, "node rostered", func() bool {
		conn, err := api.DialWSConn(ctx, gw+"/client", "", nil)
		if err != nil {
			return false
		}
		poll := api.NewClient(conn)
		defer poll.Close()
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		for _, nd := range r.Nodes {
			if nd.ID == id && nd.Online && (plaintext || nd.IdentityPubKey != "") {
				return true
			}
		}
		return false
	})
	conn, err := api.DialWSConn(ctx, gw+"/client", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var c *E2EClient
	if plaintext {
		c, err = NewE2EClientPlain(conn)
	} else {
		c, err = NewE2EClient(conn)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	awaitPath(t, c.Paths(), NodePath{NodeID: id, Via: "gateway"})
	awaitSessionEventFor(t, c, id)
	return sshFleet{c: c, n: n, sock: sock, gw: gw, ctx: ctx}
}

// awaitAddedSession reads events until an added session.event for sessionID
// arrives, stamped with nodeID.
func awaitAddedSession(t *testing.T, c *E2EClient, nodeID, sessionID string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case n := <-c.Events():
			if n.Method != api.MethodSessionEvent {
				continue
			}
			var ev registry.Event
			if err := json.Unmarshal(n.Params, &ev); err != nil {
				t.Fatalf("decode session.event: %v", err)
			}
			if ev.Session.NodeID == nodeID && strings.Contains(ev.Session.AgentSessionID, sessionID) {
				return
			}
		case <-deadline:
			t.Fatalf("no session.event for %s from %s", sessionID, nodeID)
		}
	}
}

func TestUpgradesToSSH(t *testing.T) {
	s := startSSHFleet(t, "ssh-node", "ssh-node", false)
	f := installFakeSSH(t, map[string]string{"u@box": s.sock})
	s.c.SetP2POptions(P2POptions{Enabled: true, SSH: map[string]string{"ssh-node": "ssh://u@box:2222"}})

	awaitPath(t, s.c.Paths(), NodePath{NodeID: "ssh-node", Via: "ssh"})
	if got := s.c.NodeVias()["ssh-node"]; got != "ssh" {
		t.Fatalf("via = %q, want ssh", got)
	}
	d := f.dials()
	if len(d) != 1 || d[0].dest != "u@box" || d[0].port != "2222" || strings.Join(d[0].cmd, " ") != "argus bridge" {
		t.Fatalf("ssh dials = %+v", d)
	}
	var list []session.Session
	if err := s.c.Call(api.MethodSessionsList, nil, &list); err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	if len(list) != 1 || list[0].NodeID != "ssh-node" {
		t.Fatalf("sessions = %+v", list)
	}
	// Notifications on the ssh link are stamped like relay ones.
	s.n.Registry().ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%11",
		AgentSessionID: "late-session", Status: session.StatusIdle,
	})
	awaitAddedSession(t, s.c, "ssh-node", "late-session")
}

func TestSSHPreferredOverTCP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := connectP2P(t, true, nil)
	_, sock := startSocketNode(t, ctx, "n1", "n1", &s.n.key)
	f := installFakeSSH(t, map[string]string{"box": sock})
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable, SSH: map[string]string{"N1": "ssh://box"}})

	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "ssh"})
	if s.d.dialCount() != 0 {
		t.Fatalf("tcp dialed %d times while ssh is configured", s.d.dialCount())
	}
	// SSH down: the node falls back to the relay, and the next attempt that
	// cannot use ssh takes tcp.
	f.mu.Lock()
	delete(f.sockets, "box")
	f.mu.Unlock()
	f.drop()
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
}

func TestSSHIdentityMismatchStaysOnRelay(t *testing.T) {
	s := startSSHFleet(t, "ssh-node", "ssh-node", false)
	// Another node, and one claiming the same id under a different key.
	_, other := startSocketNode(t, s.ctx, "other-node", "other-node", nil)
	kp := mustKP(t)
	_, impostor := startSocketNode(t, s.ctx, "ssh-node", "ssh-node", &kp)
	f := installFakeSSH(t, map[string]string{"other": other, "impostor": impostor})

	for _, target := range []string{"ssh://other", "ssh://impostor"} {
		before := len(f.dials())
		s.c.SetP2POptions(P2POptions{Enabled: true, SSH: map[string]string{"ssh-node": target}})
		waitUntil(t, "ssh dial to "+target, func() bool { return len(f.dials()) > before })
		noPath(t, s.c.Paths(), 400*time.Millisecond)
		if got := s.c.NodeVias()["ssh-node"]; got != "gateway" {
			t.Fatalf("%s: via = %q, want gateway", target, got)
		}
	}
	var list []session.Session
	if err := s.c.Call(api.MethodSessionsList, nil, &list); err != nil || len(list) != 1 {
		t.Fatalf("sessions.list over relay = %+v, %v", list, err)
	}
}

func TestSSHInPlaintextFleet(t *testing.T) {
	s := startSSHFleet(t, "plain-node", "Plain-Box", true)
	f := installFakeSSH(t, map[string]string{"box": s.sock})
	// Keyed by label, lowercased as the config loader does; custom command.
	s.c.SetP2POptions(P2POptions{Enabled: true, SSH: map[string]string{"plain-box": "ssh://box"}, SSHCommand: "argus-dev  bridge"})

	awaitPath(t, s.c.Paths(), NodePath{NodeID: "plain-node", Via: "ssh"})
	if d := f.dials(); len(d) != 1 || strings.Join(d[0].cmd, "|") != "argus-dev|bridge" {
		t.Fatalf("ssh dials = %+v", d)
	}
	var agents api.AgentsListResult
	if err := s.c.Call(api.MethodAgentsList, nil, &agents); err != nil {
		t.Fatalf("agents.list over ssh: %v", err)
	}
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestSSHBridgeMissingStaysOnRelay(t *testing.T) {
	s := startSSHFleet(t, "ssh-node", "ssh-node", false)
	// A real ssh run whose remote shell has no argus.
	bin := t.TempDir()
	count := filepath.Join(bin, "count")
	script := "#!/bin/sh\necho x >> " + count + "\necho 'sh: 1: argus: not found' >&2\nexit 127\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	prev := sshDial
	sshDial = sshconn.DialCommand
	t.Cleanup(func() { sshDial = prev })
	var logs syncBuffer
	prevOut := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prevOut) })

	s.c.SetP2POptions(P2POptions{Enabled: true, SSH: map[string]string{"ssh-node": "ssh://box"}})
	waitUntil(t, "ssh stderr logged", func() bool { return strings.Contains(logs.String(), "argus: not found") })
	// Retried on the relay cadence.
	waitUntil(t, "ssh retried", func() bool {
		b, _ := os.ReadFile(count)
		return strings.Count(string(b), "x") >= 2
	})
	noPath(t, s.c.Paths(), 100*time.Millisecond)
	if got := s.c.NodeVias()["ssh-node"]; got != "gateway" {
		t.Fatalf("via = %q, want gateway", got)
	}
	var list []session.Session
	if err := s.c.Call(api.MethodSessionsList, nil, &list); err != nil || len(list) != 1 {
		t.Fatalf("sessions.list over relay = %+v, %v", list, err)
	}
}

func TestSSHDropFallsBackToRelay(t *testing.T) {
	s := startSSHFleet(t, "ssh-node", "ssh-node", false)
	f := installFakeSSH(t, map[string]string{"box": s.sock})
	s.c.SetP2POptions(P2POptions{Enabled: true, SSH: map[string]string{"ssh-node": "ssh://box"}})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "ssh-node", Via: "ssh"})

	// A call in flight when the link dies fails at once.
	f.blackhole.Store(true)
	inflight := make(chan error, 1)
	go func() { inflight <- s.c.callNode("ssh-node", api.MethodAgentsList, nil, nil) }()
	time.Sleep(50 * time.Millisecond)
	dropped := time.Now()
	f.mu.Lock()
	delete(f.sockets, "box") // keep it on the relay afterwards
	f.mu.Unlock()
	f.drop()
	select {
	case err := <-inflight:
		if err == nil {
			t.Fatal("in-flight call succeeded on a dropped link")
		}
		if d := time.Since(dropped); d > time.Second {
			t.Fatalf("in-flight call failed after %v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight call did not fail")
	}

	awaitPath(t, s.c.Paths(), NodePath{NodeID: "ssh-node", Via: "gateway"})
	for _, ev := range sessionEvents(t, drainEvents(s.c, 200*time.Millisecond)) {
		if ev.Session.Offline || ev.Type == registry.EventRemoved {
			t.Fatalf("fallback surfaced %s offline=%v for %s", ev.Type, ev.Session.Offline, ev.Session.ID)
		}
	}
	var list []session.Session
	if err := s.c.Call(api.MethodSessionsList, nil, &list); err != nil || len(list) != 1 {
		t.Fatalf("sessions.list over relay = %+v, %v", list, err)
	}
}

// lockedSSHSetup is a locked-mode client on a fake gateway whose node nodeA is
// also served by a real node over ssh.
type lockedSSHSetup struct {
	c      *E2EClient
	g      *fakeMultiGateway
	nodeA  *fakeNode
	signer trustlog.SignerKey
}

func connectLockedSSH(t *testing.T) lockedSSHSetup {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	head := lg.Tip()
	self := mustKP(t)
	nodeA := newLifecycleNode(t, "nodeA", "argus:%1")
	nodeA.tip = []byte("relay-tip")
	_ = lg.AuthorizeDevice(self.Public, signer)
	_ = lg.AuthorizeDevice(nodeA.key.Public, signer)

	g, conn := newFakeMultiGateway(t, nodeA)
	g.chain = trustlog.MarshalChain(lg.Entries())
	t.Cleanup(func() { g.peer.Close() })
	c, err := NewE2EClientWithIdentity(conn, self, head, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	awaitPath(t, c.Paths(), NodePath{NodeID: "nodeA", Via: "gateway"})

	_, sock := startSocketNode(t, ctx, "nodeA", "nodeA", &nodeA.key)
	installFakeSSH(t, map[string]string{"box": sock})
	c.SetP2POptions(P2POptions{Enabled: true, SSH: map[string]string{"nodea": "ssh://box"}})
	awaitPath(t, c.Paths(), NodePath{NodeID: "nodeA", Via: "ssh"})
	return lockedSSHSetup{c: c, g: g, nodeA: nodeA, signer: signer}
}

func TestSSHClosedWhenTrustRevokesNode(t *testing.T) {
	s := connectLockedSSH(t)
	link := s.c.byNodeSnapshot()["nodeA"]
	if link == nil || link.via != "ssh" {
		t.Fatalf("link = %+v, want ssh", link)
	}
	if _, err := s.c.trust.RevokeDevice(s.nodeA.key.Public, s.signer); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	s.c.reevaluateChannels()

	awaitPath(t, s.c.Paths(), NodePath{NodeID: "nodeA"})
	if _, ok := s.c.byNodeSnapshot()["nodeA"]; ok {
		t.Fatal("revoked node still has a link")
	}
	select {
	case <-link.out.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ssh link not closed after revocation")
	}
}

func TestSSHKeepsTipState(t *testing.T) {
	s := connectLockedSSH(t)
	link := s.c.byNodeSnapshot()["nodeA"]
	if !bytes.Equal(link.identityPub, s.nodeA.key.Public) {
		t.Fatal("ssh link did not take over the node's identity key")
	}
	// The relay is gone once the switch settles: what follows travels on ssh.
	select {
	case <-s.g.relayClosed:
	case <-time.After(10 * time.Second):
		t.Fatal("relay not closed after the switch")
	}
	s.c.mu.Lock()
	connected := s.c.everConnected[string(s.nodeA.key.Public)]
	delete(s.c.authTip, string(s.nodeA.key.Public))
	s.c.mu.Unlock()
	if !connected {
		t.Fatal("everConnected lost the node")
	}
	s.c.refreshAuthTips()
	if _, ok := s.c.authTipEntry(s.nodeA.key.Public); !ok {
		t.Fatal("tip refresh over ssh did not record the node's tip")
	}
}

func TestSSHAmbiguousLabelSkipped(t *testing.T) {
	m := &E2EClient{
		p2pWarned: map[string]bool{},
		desc: map[string]api.NodeDescriptor{
			"a": {ID: "a", Label: "Box"},
			"b": {ID: "b", Label: "box"},
		},
		p2p: P2POptions{SSH: map[string]string{"box": "ssh://box", "b": "ssh://b"}},
	}
	if _, ok := m.sshTargetFor(m.desc["a"]); ok {
		t.Fatal("ambiguous label matched")
	}
	if got, ok := m.sshTargetFor(m.desc["b"]); !ok || got != "ssh://b" {
		t.Fatalf("id lookup = %q, %v", got, ok)
	}
}

// A node on tcp keeps retrying ssh while one is configured, and moves onto
// it once it answers.
func TestTCPMovesToSSHWhenItAnswers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := connectP2P(t, true, nil)
	_, sock := startSocketNode(t, ctx, "n1", "n1", &s.n.key)
	f := installFakeSSH(t, map[string]string{})
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable, SSH: map[string]string{"n1": "ssh://box"}})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	tcp := s.d.lastConn()

	f.mu.Lock()
	f.sockets["box"] = sock
	f.mu.Unlock()
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "ssh"})
	select {
	case <-tcp.peer.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("replaced tcp link still open")
	}
	noPath(t, s.c.Paths(), 3*p2pRetryInterval)
	var list []session.Session
	if err := s.c.Call(api.MethodSessionsList, nil, &list); err != nil {
		t.Fatalf("sessions.list over ssh: %v", err)
	}
}
