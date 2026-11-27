package e2etest

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/opencode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/procwalk"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// fakeAgent stands in for opencode on a node: it records the prompts the node
// delivers, the answers it relays, and the sessions it removes.
type fakeAgent struct {
	adapter.Adapter
	reg *registry.Registry

	mu        sync.Mutex
	prompts   []string
	responds  []api.RespondParams
	dismissed []string
}

func (f *fakeAgent) IsHeadless() bool  { return true }
func (f *fakeAgent) CallerEnv() string { return "OPENCODE_SESSION_ID" }
func (f *fakeAgent) IsAgentProcess(p procwalk.Proc, _ map[string]string) bool {
	return p.Runs("opencode")
}

// SpawnSession is never called: the node's spawn is replaced. It marks the
// agent as one that spawns over its own API.
func (f *fakeAgent) SpawnSession(context.Context, string, adapter.SpawnOptions) (string, error) {
	return "", nil
}

func (f *fakeAgent) SendPrompt(_ context.Context, _ session.Session, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, text)
	return nil
}

func (f *fakeAgent) Respond(_ context.Context, _ session.Session, p api.RespondParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responds = append(f.responds, p)
	return nil
}

// Dismiss removes every opencode session of the node; each test node holds one.
func (f *fakeAgent) Dismiss(_ context.Context, s session.Session) error {
	f.mu.Lock()
	f.dismissed = append(f.dismissed, s.AgentSessionID)
	f.mu.Unlock()
	f.reg.ReconcileSessions(opencode.Agent, nil)
	return nil
}

// promptWith returns the first delivered prompt that contains s.
func (f *fakeAgent) promptWith(s string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.prompts {
		if strings.Contains(p, s) {
			return p, true
		}
	}
	return "", false
}

func (f *fakeAgent) respondsCopy() []api.RespondParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.responds)
}

// agentProcs is a process table where pid 120 runs under an opencode process,
// so a caller with pid 120 is the opencode session its env names.
func agentProcs(context.Context) (procwalk.Table, error) {
	return procwalk.Table{
		1:   {PID: 1, Args: "/sbin/launchd"},
		100: {PID: 100, PPID: 1, Args: "opencode", Exec: "opencode"},
		120: {PID: 120, PPID: 100, Args: "argus session"},
	}, nil
}

func callerOf(agentSessionID string) api.CallerInfo {
	return api.CallerInfo{PID: 120, Env: map[string]string{"OPENCODE_SESSION_ID": agentSessionID}}
}

// orchNode is a node uplinked to the gateway with one fake opencode session.
type orchNode struct {
	n     *node.Node
	store *orch.Store
	agent *fakeAgent
	sid   string // composite ID of its opencode session

	mu           sync.Mutex
	spawnPrompts []string
}

type crossMode int

const (
	modeE2EE crossMode = iota
	modePlaintext
	modeLocked
)

type crossOpts struct {
	mode crossMode
	// unauthB leaves b's identity key out of the trust log (locked mode).
	unauthB bool
	// direct makes both nodes listen for direct links and advertise them.
	direct bool
}

// crossNet is a real gateway with nodes a and b. a holds the parent opencode
// session p1; b spawns the child c1.
type crossNet struct {
	url      string
	a, b     *orchNode
	genesis  []byte
	chain    []byte
	clientKP e2e.KeyPair
}

func newCrossNet(t *testing.T, opts crossOpts) *crossNet {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ts := startGateway(t)
	cn := &crossNet{url: ts.URL}

	keyA, keyB := mustCrossKey(t), mustCrossKey(t)
	if opts.mode == modeLocked {
		cn.clientKP = mustCrossKey(t)
		signer, err := trustlog.GenerateSigner()
		if err != nil {
			t.Fatalf("signer: %v", err)
		}
		tlog, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
		if err != nil {
			t.Fatalf("genesis: %v", err)
		}
		cn.genesis = tlog.Tip()
		devices := [][]byte{keyA.Public, cn.clientKP.Public}
		if !opts.unauthB {
			devices = append(devices, keyB.Public)
		}
		for _, dev := range devices {
			if err := tlog.AuthorizeDevice(dev, signer); err != nil {
				t.Fatalf("authorize: %v", err)
			}
		}
		cn.chain = trustlog.MarshalChain(tlog.Entries())
	}

	cn.a = startOrchNode(t, ctx, cn, "a", keyA, opts, "p1")
	cn.b = startOrchNode(t, ctx, cn, "b", keyB, opts, "")
	if opts.mode != modePlaintext {
		waitRostered(t, ctx, cn.url, "a")
		waitRostered(t, ctx, cn.url, "b")
	}
	// In plaintext mode, waitPeers alone waits for the roster.
	return cn
}

func mustCrossKey(t *testing.T) e2e.KeyPair {
	t.Helper()
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	return kp
}

// startOrchNode brings up node id with an orch store and the fake agent. A
// non-empty session registers that opencode session at once; otherwise the
// node gets a fake spawner that registers c1.
func startOrchNode(t *testing.T, ctx context.Context, cn *crossNet, id string, kp e2e.KeyPair, opts crossOpts, sess string) *orchNode {
	t.Helper()
	mode := opts.mode
	n := node.New()
	n.SetIdentity(id, id)
	n.SetVersion("itest")
	if mode != modePlaintext {
		n.SetIdentityKey(kp)
		n.SetE2EE(true)
	}
	dir := t.TempDir()
	if mode == modeLocked {
		path := filepath.Join(dir, "trustlog")
		if err := os.WriteFile(path, cn.chain, 0o600); err != nil {
			t.Fatalf("write chain: %v", err)
		}
		if err := n.EnableTrustLog(cn.genesis, path); err != nil {
			t.Fatalf("EnableTrustLog %s: %v", id, err)
		}
	}
	sqlDB, err := db.Open(filepath.Join(dir, "argus.db"))
	if err != nil {
		t.Fatalf("db %s: %v", id, err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	store := orch.NewStore(sqlDB, time.Now)
	n.SetOrchStore(store)

	on := &orchNode{n: n, store: store, agent: &fakeAgent{Adapter: opencode.New(), reg: n.Registry()}}
	n.SetAdapterForTest(opencode.Agent, on.agent)
	n.SetProcTableForTest(agentProcs)
	n.SetSpawnForTest(func(_ context.Context, p api.SpawnParams, _ string) (api.SpawnResult, error) {
		on.mu.Lock()
		on.spawnPrompts = append(on.spawnPrompts, p.Prompt)
		on.mu.Unlock()
		s := applyAgent(n, "c1", session.StatusIdle, nil)
		return api.SpawnResult{SessionID: s.ID}, nil
	})
	// A bystander session, so a node's sessions show in a peer's list before
	// any spawn.
	n.Registry().ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%9" + id,
		AgentSessionID: "w-" + id, Status: session.StatusIdle,
	})
	if sess != "" {
		on.sid = session.CompositeID(id, applyAgent(n, sess, session.StatusIdle, nil).ID)
	}

	var p2pAddr string
	if opts.direct {
		// Nodes share the master token; a plaintext node presents it to open a
		// direct peer channel.
		n.SetMasterToken("orch-master")
		n.SetTailnet(tailnetStub{})
		p2pAddr = freeAddr(t)
		n.SetP2PEndpoints([]string{"ws://" + p2pAddr + "/p2p"})
	}

	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.ConnectGateway(ctx, wsURL(cn.url, "/node"), "", nil)
	}()
	if opts.direct {
		n.ListenP2P(ctx, []string{p2pAddr})
	}
	wait := n.StartOrchForTest(ctx)
	// Registered after the db's Close, so it runs first: the loops stop before
	// the db closes.
	t.Cleanup(func() { cancel(); <-done; wait() })
	return on
}

func applyAgent(n *node.Node, id string, status session.Status, ix *session.Interaction) session.Session {
	s, _ := n.Registry().ApplyHook(registry.HookUpdate{
		Agent: opencode.Agent, AgentSessionID: id, Status: status, Interaction: ix,
		Input: session.InputAPI, ReplaceInteraction: true,
	})
	return s
}

// call runs an orch.* method on o's local API, as an agent tool does.
func (o *orchNode) call(t *testing.T, method string, params, out any) error {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := o.n.LocalDispatchForTest()(ctx, method, raw)
	if err != nil || out == nil {
		return err
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return json.Unmarshal(b, out)
}

// listAll is orch.session.list --all as the agent session agentSessionID.
func (o *orchNode) listAll(t *testing.T, agentSessionID string) (api.OrchListResult, error) {
	t.Helper()
	var res api.OrchListResult
	err := o.call(t, api.MethodOrchSessionList, api.OrchListParams{Caller: callerOf(agentSessionID), All: true}, &res)
	return res, err
}

func hasSessionOn(res api.OrchListResult, nodeID string) bool {
	for _, s := range res.Sessions {
		if s.NodeID == nodeID || strings.HasPrefix(s.ID, nodeID+":") {
			return true
		}
	}
	return false
}

// waitPeers blocks until a's parent lists b's sessions over a peer channel.
func (cn *crossNet) waitPeers(t *testing.T) {
	t.Helper()
	waitFor(t, "a reaches b", func() bool {
		res, err := cn.a.listAll(t, "p1")
		return err == nil && len(res.Unreachable) == 0 && hasSessionOn(res, "b")
	})
}

func (o *orchNode) link(t *testing.T, child api.Actor) (api.Link, bool) {
	t.Helper()
	l, ok, err := o.store.Link(context.Background(), child)
	if err != nil {
		t.Fatalf("link %+v: %v", child, err)
	}
	return l, ok
}

func (o *orchNode) message(t *testing.T, id string) (api.Message, bool) {
	t.Helper()
	m, ok, err := o.store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("message %s: %v", id, err)
	}
	return m, ok
}

var noticeIDRe = regexp.MustCompile(`id="(b:m-[^"]+)"`)

// runFlow drives the cross-node flow: the parent p1 on a spawns c1 on b, gets
// c1's prompt notice, answers it, gets c1's reply, and kills c1. mid runs
// after the spawn and before the kill.
func runFlow(t *testing.T, cn *crossNet, mid func()) {
	t.Helper()
	a, b := cn.a, cn.b
	cn.waitPeers(t)

	// Spawn on b.
	var spawned api.OrchSpawnResult
	if err := a.call(t, api.MethodOrchSessionSpawn, api.OrchSpawnParams{
		Caller: callerOf("p1"), Agent: opencode.Agent, Cwd: "/tmp", Prompt: "build it", Node: "b",
	}, &spawned); err != nil {
		t.Fatalf("spawn on b: %v", err)
	}
	const childSID = "b:opencode:c1"
	if _, ok := b.n.Registry().FindByAgentSession(opencode.Agent, "c1"); !ok {
		t.Fatal("spawn did not reach b's spawner")
	}
	if spawned.SessionID != childSID || !strings.HasPrefix(spawned.TaskID, "b:m-") {
		t.Fatalf("spawn result = %+v, want session %s", spawned, childSID)
	}
	b.mu.Lock()
	spawnPrompt := strings.Join(b.spawnPrompts, "\n")
	b.mu.Unlock()
	if !strings.Contains(spawnPrompt, "build it") || !strings.Contains(spawnPrompt, a.sid) {
		t.Fatalf("child's first prompt = %q", spawnPrompt)
	}
	childKey := "opencode:c1"
	parent := api.Actor{Node: "a", Key: "opencode:p1"}
	if l, ok := b.link(t, api.Actor{Node: "b", Key: childKey}); !ok || l.Parent != parent || l.TaskID != spawned.TaskID {
		t.Fatalf("link on b = %+v ok=%v", l, ok)
	}
	if l, ok := a.link(t, api.Actor{Node: "b", Key: childKey}); !ok || l.Child != (api.Actor{Node: "b", Key: childKey}) {
		t.Fatalf("mirror on a = %+v ok=%v", l, ok)
	}

	if mid != nil {
		mid()
	}

	// c1 asks for a permission: b sends a its notice, and a delivers it to p1.
	ix := &session.Interaction{Kind: session.InteractionQuestion, Message: "go test ./...", RequestID: "r1", Questions: []session.QuestionSpec{{Question: "Proceed?", Options: []string{"Yes", "No"}}}}
	applyAgent(b.n, "c1", session.StatusAwaitingInput, ix)
	var notice string
	waitFor(t, "prompt notice reaches p1", func() bool {
		p, ok := a.agent.promptWith("go test ./...")
		if !ok {
			return false
		}
		if m := noticeIDRe.FindStringSubmatch(p); m != nil {
			notice = m[1]
		}
		return notice != ""
	})
	if m, ok := a.message(t, notice); !ok || m.Kind != api.MessageInteraction || m.To != parent {
		t.Fatalf("notice on a = %+v ok=%v", m, ok)
	}
	// p1 takes its turn.
	applyAgent(a.n, "p1", session.StatusWorking, nil)

	if err := a.call(t, api.MethodOrchInteractionRespond, api.OrchRespondParams{
		Caller: callerOf("p1"), MessageID: notice, Answers: map[string]any{"Proceed?": "Yes"},
	}, nil); err != nil {
		t.Fatalf("respond: %v", err)
	}
	rs := b.agent.respondsCopy()
	if len(rs) != 1 || rs[0].Answers["Proceed?"] != "Yes" || rs[0].RequestID != "r1" || session.CompositeID("b", rs[0].SessionID) != childSID {
		t.Fatalf("child got answers %+v", rs)
	}
	if m, _ := b.message(t, notice); m.State != api.MessageReplied || m.AnsweredBy == nil || *m.AnsweredBy != (api.Actor{Node: "a", Key: "opencode:p1"}) {
		t.Fatalf("notice on b = %+v", m)
	}
	if m, _ := a.message(t, notice); m.State != api.MessageReplied {
		t.Fatalf("notice copy on a = %+v", m)
	}

	// c1 replies to its task; the reply reaches p1 once its turn ends.
	applyAgent(b.n, "c1", session.StatusWorking, nil)
	var reply api.OrchSendResult
	if err := b.call(t, api.MethodOrchMessageReply, api.OrchReplyParams{
		Caller: callerOf("c1"), MessageID: spawned.TaskID, Text: "built and tested",
	}, &reply); err != nil {
		t.Fatalf("reply: %v", err)
	}
	applyAgent(a.n, "p1", session.StatusIdle, nil)
	waitFor(t, "reply reaches p1", func() bool {
		p, ok := a.agent.promptWith("built and tested")
		return ok && strings.Contains(p, reply.MessageID)
	})
	waitFor(t, "reply delivered on a", func() bool {
		m, ok := a.message(t, reply.MessageID)
		return ok && m.ReplyTo == spawned.TaskID && m.State == api.MessageDelivered
	})

	// p1 kills c1.
	var killed api.OrchKillResult
	if err := a.call(t, api.MethodOrchSessionKill, api.OrchKillParams{
		OrchTargetParams: api.OrchTargetParams{Caller: callerOf("p1"), SessionID: childSID},
	}, &killed); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if !slices.Equal(killed.Killed, []string{childSID}) || len(killed.NotKilled) != 0 {
		t.Fatalf("kill result = %+v", killed)
	}
	if _, ok := b.n.Registry().FindByAgentSession(opencode.Agent, "c1"); ok {
		t.Fatal("c1 still live on b")
	}
	waitFor(t, "b's link ends", func() bool {
		l, _ := b.link(t, api.Actor{Node: "b", Key: childKey})
		return l.EndedBy == "parent"
	})
	waitFor(t, "a's mirror ends", func() bool {
		l, _ := a.link(t, api.Actor{Node: "b", Key: childKey})
		return l.EndedBy == "parent"
	})
}

func TestCrossNodeOrchE2EE(t *testing.T) {
	runFlow(t, newCrossNet(t, crossOpts{mode: modeE2EE}), nil)
}

func TestCrossNodeOrchPlaintext(t *testing.T) {
	runFlow(t, newCrossNet(t, crossOpts{mode: modePlaintext}), nil)
}

func TestCrossNodeOrchLocked(t *testing.T) {
	runFlow(t, newCrossNet(t, crossOpts{mode: modeLocked}), nil)
}

// tailnetStub is a running tailscaled that names every address a peer.
type tailnetStub struct{}

func (tailnetStub) WhoIs(context.Context, string) (*apitype.WhoIsResponse, error) {
	return &apitype.WhoIsResponse{Node: &tailcfg.Node{StableID: "peer"}}, nil
}

func (tailnetStub) Status(context.Context) (*ipnstate.Status, error) {
	return &ipnstate.Status{BackendState: "Running"}, nil
}

// freeAddr returns a loopback address with a port nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// TestCrossNodeOrchDirect runs the cross-node flow with both nodes listening
// for direct links: the nodes call each other directly, and the gateway
// carries no channel between them.
func TestCrossNodeOrchDirect(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode crossMode
	}{{"e2ee", modeE2EE}, {"plaintext", modePlaintext}, {"locked", modeLocked}} {
		t.Run(tc.name, func(t *testing.T) {
			cn := newCrossNet(t, crossOpts{mode: tc.mode, direct: true})
			runFlow(t, cn, nil)
			for _, p := range []struct {
				from *orchNode
				to   string
			}{{cn.a, "b"}, {cn.b, "a"}} {
				if via := p.from.n.PeerViaForTest(p.to); via != "tcp" {
					t.Errorf("peer channel to %s is on %q, want tcp", p.to, via)
				}
			}
			if n := cn.a.n.RelayChannelCountForTest() + cn.b.n.RelayChannelCountForTest(); n != 0 {
				t.Errorf("nodes terminate %d relay channels, want 0", n)
			}
		})
	}
}

// lockedClient connects a locked-mode client with the authorized client key.
func (cn *crossNet) lockedClient(t *testing.T) *client.ReconnectingE2EClient {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client-trustlog")
	if err := os.WriteFile(path, cn.chain, 0o600); err != nil {
		t.Fatalf("write client chain: %v", err)
	}
	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(cn.url, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClientLocked(context.Background(), dial, cn.genesis, cn.clientKP, path)
	if err != nil {
		t.Fatalf("locked client: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestCrossNodeOrchLockedUnauthorizedNode(t *testing.T) {
	cn := newCrossNet(t, crossOpts{mode: modeLocked, unauthB: true})
	c1 := session.CompositeID("b", applyAgent(cn.b.n, "c1", session.StatusIdle, nil).ID)

	// b's roster key is not authorized, so a opens no channel to b: the key
	// can be one that the gateway substituted.
	waitFor(t, "a sees b unreachable", func() bool {
		res, err := cn.a.listAll(t, "p1")
		return err == nil && slices.Equal(res.Unreachable, []string{"b"})
	})
	err := cn.a.call(t, api.MethodOrchMessageSend, api.OrchSendParams{
		Caller: callerOf("p1"), SessionID: c1, Text: "hello",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("a's send to b: err = %v, want a refused open", err)
	}
	if got := cn.b.n.RelayChannelCountForTest(); got != 0 {
		t.Fatalf("b terminates %d channels, want 0", got)
	}

	// a refuses b's channel, so b's calls to a fail.
	err = cn.b.call(t, api.MethodOrchMessageSend, api.OrchSendParams{
		Caller: callerOf("c1"), SessionID: cn.a.sid, Text: "hello",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "handshake") {
		t.Fatalf("b's send to a: err = %v, want a handshake failure", err)
	}
	res, err := cn.b.listAll(t, "c1")
	if err != nil {
		t.Fatalf("list on b: %v", err)
	}
	if !slices.Equal(res.Unreachable, []string{"a"}) || hasSessionOn(res, "a") {
		t.Fatalf("b's list = %+v, want a unreachable", res)
	}
	if got := cn.a.n.RelayChannelCountForTest(); got != 0 {
		t.Fatalf("a terminates %d channels, want 0", got)
	}

	// a's client channels still work.
	c := cn.lockedClient(t)
	var list []session.Session
	waitFor(t, "client lists a's session", func() bool {
		list = nil
		if c.Call(api.MethodSessionsList, nil, &list) != nil {
			return false
		}
		return slices.ContainsFunc(list, func(s session.Session) bool { return s.ID == cn.a.sid })
	})
	var agents api.AgentsListResult
	if err := c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "a"}, &agents); err != nil {
		t.Fatalf("agents.list on a: %v", err)
	}
}

func TestCrossNodeClientRelayUnchanged(t *testing.T) {
	cn := newCrossNet(t, crossOpts{mode: modeE2EE})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := connectClient(t, ctx, cn.url)

	sessionsOf := func() map[string]session.Session {
		var list []session.Session
		if err := c.Call(api.MethodSessionsList, nil, &list); err != nil {
			return nil
		}
		out := map[string]session.Session{}
		for _, s := range list {
			out[s.ID] = s
		}
		return out
	}
	read := func(sid string) {
		t.Helper()
		var view json.RawMessage
		if err := c.Call(api.MethodSessionTranscriptView, api.TranscriptParams{SessionID: sid}, &view); err != nil {
			t.Fatalf("transcriptView %s: %v", sid, err)
		}
	}

	const childSID = "b:opencode:c1"
	runFlow(t, cn, func() {
		waitFor(t, "client lists both nodes' sessions", func() bool {
			got := sessionsOf()
			ch, ok := got[childSID]
			_, hasParent := got[cn.a.sid]
			return ok && hasParent && ch.Parent != nil && ch.Parent.ID == cn.a.sid
		})
		read(cn.a.sid)
		read(childSID)
		// a serves the client and b's peer channel; b serves the client and a's.
		if n := cn.a.n.RelayChannelCountForTest(); n != 1 {
			t.Fatalf("a terminates %d channels, want 1 (the client's)", n)
		}
		if n := cn.b.n.RelayChannelCountForTest(); n != 2 {
			t.Fatalf("b terminates %d channels, want 2 (the client's and a's)", n)
		}
	})
	waitFor(t, "client drops the killed child", func() bool {
		got := sessionsOf()
		_, child := got[childSID]
		_, parent := got[cn.a.sid]
		return got != nil && !child && parent
	})
	read(cn.a.sid)
}

// An unlinked send and its reply carry the sender's session, so each side can
// address the other.
func TestCrossNodeUnlinkedSendCarriesSender(t *testing.T) {
	cn := newCrossNet(t, crossOpts{mode: modeE2EE})
	c1 := session.CompositeID("b", applyAgent(cn.b.n, "c1", session.StatusIdle, nil).ID)
	cn.waitPeers(t)

	var sent api.OrchSendResult
	if err := cn.a.call(t, api.MethodOrchMessageSend, api.OrchSendParams{
		Caller: callerOf("p1"), SessionID: c1, Text: "hello from p1",
	}, &sent); err != nil {
		t.Fatalf("send to b: %v", err)
	}
	var got string
	waitFor(t, "send reaches c1", func() bool {
		got, _ = cn.b.agent.promptWith("hello from p1")
		return got != ""
	})
	if !strings.Contains(got, `session="`+cn.a.sid+`"`) || strings.Contains(got, `from="a:`) {
		t.Fatalf("c1's prompt = %q, want p1's label and session %s", got, cn.a.sid)
	}

	if err := cn.b.call(t, api.MethodOrchMessageReply, api.OrchReplyParams{
		Caller: callerOf("c1"), MessageID: sent.MessageID, Text: "hello back",
	}, nil); err != nil {
		t.Fatalf("reply: %v", err)
	}
	waitFor(t, "reply reaches p1", func() bool {
		got, _ = cn.a.agent.promptWith("hello back")
		return got != ""
	})
	if !strings.Contains(got, `session="`+c1+`"`) || strings.Contains(got, `from="b:`) {
		t.Fatalf("p1's prompt = %q, want c1's label and session %s", got, c1)
	}
}
