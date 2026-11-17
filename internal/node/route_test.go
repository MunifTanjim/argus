package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/session"
)

func withOrchStore(t *testing.T, d *Node) {
	t.Helper()
	sqlDB, err := db.Open(t.TempDir() + "/argus.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	d.SetOrchStore(orch.NewStore(sqlDB, time.Now))
	d.adapterList = adapters.All()
	d.adapters["opencode"] = orchPrompter{&fakePrompter{}}
}

// routePeers is two rostered nodes with agent sessions: caller (opencode) on a,
// and worker (opencode) and child (claude, default:%p2) on b.
type routePeers struct {
	a, b           *Node
	stopB          func()
	caller, worker session.Session
	fd             *fakeDismisser
}

func newRoutePeers(t *testing.T) routePeers {
	t.Helper()
	url := startPeerGateway(t)
	var rp routePeers
	rp.a, _ = startPeerNodeWith(t, url, "a", mustKeyPair(t), true, func(d *Node) {
		withOrchStore(t, d)
		rp.caller = apiSession(d, "p1", session.StatusIdle, nil)
	})
	rp.fd = &fakeDismisser{}
	rp.b, rp.stopB = startPeerNodeWith(t, url, "b", mustKeyPair(t), true, func(d *Node) {
		withOrchStore(t, d)
		d.adapters["claude"] = rp.fd
		rp.worker = apiSession(d, "w1", session.StatusWorking, nil)
		d.addAgentSession("claude", "p2")
	})
	waitPeerOnline(t, rp.a, "b", true)
	waitPeerOnline(t, rp.b, "a", true)
	return rp
}

func TestRouteShowRemote(t *testing.T) {
	rp := newRoutePeers(t)
	id := session.CompositeID("b", rp.worker.ID)
	params, _ := json.Marshal(api.OrchTargetParams{SessionID: id})
	out, err := rp.a.handleOrchSessionGet(peerCtx(t), params)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(api.OrchGetResult).Session; got.ID != id || got.AgentSessionID != "w1" {
		t.Fatalf("session = %+v", got)
	}
}

func TestRouteSendRemote(t *testing.T) {
	rp := newRoutePeers(t)
	ctx := peerCtx(t)
	res, err := rp.a.send(ctx, rp.a.localCallerRef(rp.caller), session.CompositeID("b", rp.worker.ID), "hi", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.MessageID, "a:m-") {
		t.Fatalf("message id = %q, want one created on a", res.MessageID)
	}
	if _, ok, _ := rp.a.orch.Get(ctx, res.MessageID); !ok {
		t.Fatal("a does not own the message")
	}
	waitFor(t, func() bool {
		rp.a.flushOutbox(ctx)
		_, ok, _ := rp.b.orch.Get(ctx, res.MessageID)
		return ok
	})
	m, ok, err := rp.b.orch.Get(ctx, res.MessageID)
	if err != nil || !ok {
		t.Fatalf("b has no message %s: ok=%v err=%v", res.MessageID, ok, err)
	}
	if want := (api.Actor{Node: "a", Key: "opencode:p1"}); m.From != want {
		t.Fatalf("from = %+v, want %+v", m.From, want)
	}
	if m.To != rp.b.actor(rp.worker) || m.Body != "hi" {
		t.Fatalf("message = %+v", m)
	}
}

func TestRouteKillRemoteChild(t *testing.T) {
	rp := newRoutePeers(t)
	ctx := peerCtx(t)
	if err := rp.b.orch.PutLink(ctx, api.Link{
		Child:  api.Actor{Node: "b", Key: "claude:p2"},
		Parent: api.Actor{Node: "a", Key: "opencode:p1"},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := rp.a.kill(ctx, rp.a.localCallerRef(rp.caller), "b:default:%p2")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Killed) != 1 || res.Killed[0] != "b:default:%p2" {
		t.Fatalf("killed = %v", res.Killed)
	}
	if !rp.fd.called {
		t.Fatal("child was not dismissed")
	}
}

func TestRouteKillRemoteNotParent(t *testing.T) {
	rp := newRoutePeers(t)
	_, err := rp.a.kill(peerCtx(t), rp.a.localCallerRef(rp.caller), "b:default:%p2")
	if err == nil || !strings.Contains(err.Error(), "only the parent") {
		t.Fatalf("err = %v", err)
	}
	if rp.fd.called {
		t.Fatal("child was dismissed")
	}
}

func TestRouteOfflineNodeFailsAtOnce(t *testing.T) {
	rp := newRoutePeers(t)
	rp.stopB()
	waitFor(t, func() bool {
		n, ok := rp.a.roster.get("b")
		return !ok || !n.Online
	})
	start := time.Now()
	_, err := rp.a.send(peerCtx(t), rp.a.localCallerRef(rp.caller), session.CompositeID("b", rp.worker.ID), "hi", "")
	if el := time.Since(start); el > time.Second {
		t.Fatalf("send took %v", el)
	}
	if err == nil || !strings.Contains(err.Error(), "node b") {
		t.Fatalf("err = %v, want it to name node b", err)
	}
}

func TestRouteOldPeerWithoutCapability(t *testing.T) {
	rp := newRoutePeers(t)
	old, _ := rp.a.roster.get("b")
	old.Capabilities = api.NodeCapabilities{SpawnSession: true}
	rp.a.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: old})
	_, err := rp.a.getSession(peerCtx(t), session.CompositeID("b", rp.worker.ID))
	if err == nil || !strings.Contains(err.Error(), "node b does not support cross-node orchestration") {
		t.Fatalf("err = %v", err)
	}
}

func TestRouteTargetBareIDThatLooksComposite(t *testing.T) {
	d, _ := orchNode(t)
	d.SetIdentity("home", "")
	d.addAgentSession("claude", "3")
	if _, remote := d.ownerOf("default:%3"); remote {
		t.Fatal("default:%3 routed to node default")
	}
	res, err := d.getSession(t.Context(), "default:%3")
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.ID != "home:default:%3" {
		t.Fatalf("session = %+v", res.Session)
	}
}

func TestRouteNeverForwardsFromPeer(t *testing.T) {
	rp := newRoutePeers(t)
	ctx := withPeerNode(peerCtx(t), "b")
	_, err := rp.a.getSession(ctx, session.CompositeID("b", rp.worker.ID))
	if err == nil || !strings.Contains(err.Error(), "not on node a") {
		t.Fatalf("err = %v", err)
	}
}

func TestOwnerOf(t *testing.T) {
	d := newNode(nil)
	d.SetIdentity("home", "")
	fakeRosterEntry(t, d, "b")
	for _, tc := range []struct {
		id     string
		node   string
		remote bool
	}{
		{"b:argus:%1", "b", true},
		{"b:m-00000001", "b", true},
		{"home:argus:%1", "home", false},
		{"default:%3", "home", false},
		{"m-00000001", "home", false},
		{"zzz:argus:%1", "home", false},
	} {
		node, remote := d.ownerOf(tc.id)
		if node != tc.node || remote != tc.remote {
			t.Errorf("ownerOf(%q) = %q, %v; want %q, %v", tc.id, node, remote, tc.node, tc.remote)
		}
	}
}

func TestForwardOfflineIsErrPeerOffline(t *testing.T) {
	d := newNode(nil)
	d.SetIdentity("home", "")
	d.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{
		ID: "b", Capabilities: api.NodeCapabilities{CrossNodeOrch: crossNodeOrchVersion},
	}})
	err := d.forward(context.Background(), "b", api.MethodPing, nil, nil)
	if !errors.Is(err, errPeerOffline) || !strings.Contains(err.Error(), "node b") {
		t.Fatalf("err = %v", err)
	}
}

func TestNodeAdvertisesCrossNodeOrch(t *testing.T) {
	if got := newNode(nil).Capabilities().CrossNodeOrch; got != 2 {
		t.Fatalf("CrossNodeOrch = %d, want 2", got)
	}
}

func TestCallerFromRejectsReservedRemoteKey(t *testing.T) {
	d := callerNode()
	d.SetIdentity("home", "")
	for _, key := range []string{api.ActorOwner, api.ActorArgus} {
		_, err := d.callerFrom(withPeerNode(t.Context(), "b"), remoteCaller("b", key))
		if !errors.Is(err, errRemoteCallerDenied) {
			t.Fatalf("%s: err = %v", key, err)
		}
	}
}

func idsOf(ss []session.Session) []string {
	ids := make([]string, 0, len(ss))
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestListAllMergesNodes(t *testing.T) {
	rp := newRoutePeers(t)
	res := rp.a.listAll(peerCtx(t))
	if len(res.Unreachable) != 0 {
		t.Fatalf("unreachable = %v", res.Unreachable)
	}
	var onA, onB int
	for _, s := range res.Sessions {
		switch {
		case strings.HasPrefix(s.ID, "a:"):
			onA++
		case strings.HasPrefix(s.ID, "b:"):
			onB++
		default:
			t.Fatalf("unprefixed id %q", s.ID)
		}
	}
	if onA != 1 || onB != 2 {
		t.Fatalf("a=%d b=%d ids=%v", onA, onB, idsOf(res.Sessions))
	}
}

func TestListAllSlowPeerTimesOut(t *testing.T) {
	rp := newRoutePeers(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	rp.b.server.Handle(api.MethodOrchSessionList, func(ctx context.Context, _ json.RawMessage) (any, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return api.OrchListResult{}, nil
	})
	start := time.Now()
	res := rp.a.listAll(peerCtx(t))
	if took := time.Since(start); took >= 4*time.Second {
		t.Fatalf("took %v", took)
	}
	if len(res.Unreachable) != 1 || res.Unreachable[0] != "b" {
		t.Fatalf("unreachable = %v", res.Unreachable)
	}
	if len(res.Sessions) != 1 || !strings.HasPrefix(res.Sessions[0].ID, "a:") {
		t.Fatalf("sessions = %v", idsOf(res.Sessions))
	}
}
func TestListAllReportsOfflineNodes(t *testing.T) {
	rp := newRoutePeers(t)
	rp.a.roster.apply(api.NodeEvent{Type: api.NodeEventOffline, Node: api.NodeDescriptor{ID: "b"}})
	res := rp.a.listAll(peerCtx(t))
	if len(res.Unreachable) != 1 || res.Unreachable[0] != "b" {
		t.Fatalf("unreachable = %v", res.Unreachable)
	}
	if len(res.Sessions) != 1 || !strings.HasPrefix(res.Sessions[0].ID, "a:") {
		t.Fatalf("sessions = %v", idsOf(res.Sessions))
	}
}

func TestListAllReturnsWhenHandshakeHangs(t *testing.T) {
	rp := newRoutePeers(t)
	// a holds the wrong key for b, so b drops a's msg1 and never answers.
	rp.a.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{
		ID: "b", Online: true, IdentityPubKey: base64.StdEncoding.EncodeToString(mustKeyPair(t).Public),
		Capabilities: api.NodeCapabilities{CrossNodeOrch: crossNodeOrchVersion},
	}})
	start := time.Now()
	res := rp.a.listAll(peerCtx(t))
	if took := time.Since(start); took >= 4*time.Second {
		t.Fatalf("took %v", took)
	}
	if len(res.Unreachable) != 1 || res.Unreachable[0] != "b" {
		t.Fatalf("unreachable = %v", res.Unreachable)
	}
}

func TestListLocalOnlyForPeers(t *testing.T) {
	rp := newRoutePeers(t)
	params, _ := json.Marshal(api.OrchListParams{All: true})
	out, err := rp.a.handleOrchSessionList(withPeerNode(peerCtx(t), "b"), params)
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(api.OrchListResult); len(res.Sessions) != 1 || len(res.Unreachable) != 0 {
		t.Fatalf("peer All=true: %v", idsOf(res.Sessions))
	}
	out, err = rp.a.handleOrchSessionList(peerCtx(t), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(api.OrchListResult); len(res.Sessions) != 1 {
		t.Fatalf("All=false: %v", idsOf(res.Sessions))
	}
	out, err = rp.a.handleOrchSessionList(peerCtx(t), params)
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(api.OrchListResult); len(res.Sessions) != 3 {
		t.Fatalf("local All=true: %v", idsOf(res.Sessions))
	}
}

// spawnPeers is two rostered nodes: caller (opencode) on a, and a fake headless
// spawner on b, whose label is labelB.
type spawnPeers struct {
	a, b   *Node
	caller session.Session
	fs     *fakeSpawner
	cwd    string
}

func newSpawnPeers(t *testing.T, labelB string) spawnPeers {
	t.Helper()
	url := startPeerGateway(t)
	sp := spawnPeers{cwd: t.TempDir()}
	sp.a, _ = startPeerNodeWith(t, url, "a", mustKeyPair(t), true, func(d *Node) {
		withOrchStore(t, d)
		sp.caller = apiSession(d, "p1", session.StatusIdle, nil)
	})
	sp.fs = &fakeSpawner{id: "ses_x"}
	sp.b, _ = startPeerNodeWith(t, url, "b", mustKeyPair(t), true, func(d *Node) {
		withOrchStore(t, d)
		d.SetIdentity("b", labelB)
		d.adapters["fake"] = sp.fs
	})
	waitPeerOnline(t, sp.a, "b", true)
	waitPeerOnline(t, sp.b, "a", true)
	return sp
}

func (sp spawnPeers) spawn(t *testing.T, node, requestID string) (api.OrchSpawnResult, error) {
	t.Helper()
	return sp.a.spawnFor(peerCtx(t), sp.a.localCallerRef(sp.caller), api.OrchSpawnParams{
		Agent: "fake", Cwd: sp.cwd, Prompt: "build it", Node: node, RequestID: requestID,
	})
}

func TestSpawnRemoteNode(t *testing.T) {
	sp := newSpawnPeers(t, "")
	res, err := sp.spawn(t, "b", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "b:fake:ses_x" || !strings.HasPrefix(res.TaskID, "b:m-") || res.ChildKey != "" {
		t.Fatalf("result = %+v", res)
	}
	parent := api.Actor{Node: "a", Key: "opencode:p1"}
	want := api.Link{Child: api.Actor{Node: "b", Key: "fake:ses_x"}, Parent: parent, TaskID: res.TaskID, ChildSessionID: res.SessionID}
	want.ParentSessionID = "a:" + sp.caller.ID
	link, ok, err := sp.b.orch.Link(t.Context(), api.Actor{Node: "b", Key: "fake:ses_x"})
	if err != nil || !ok || link != want {
		t.Fatalf("b link = %+v ok=%v err=%v, want %+v", link, ok, err, want)
	}
	want.ParentSessionID = ""
	mirror, ok, err := sp.a.orch.Link(t.Context(), api.Actor{Node: "b", Key: "fake:ses_x"})
	if err != nil || !ok || mirror != want {
		t.Fatalf("a mirror = %+v ok=%v err=%v, want %+v", mirror, ok, err, want)
	}
	if !strings.Contains(sp.fs.gotPrompt, "build it") || !strings.Contains(sp.fs.gotPrompt, "a:"+sp.caller.ID) {
		t.Fatalf("prompt = %q", sp.fs.gotPrompt)
	}
}

func TestSpawnByNodeLabel(t *testing.T) {
	sp := newSpawnPeers(t, "bee")
	waitFor(t, func() bool {
		n, ok := sp.a.roster.get("b")
		return ok && n.Label == "bee"
	})
	res, err := sp.spawn(t, "bee", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "b:fake:ses_x" {
		t.Fatalf("session id = %q", res.SessionID)
	}
}

func TestSpawnUnknownNode(t *testing.T) {
	d, fs, caller := spawnNode(t, true)
	params, _ := json.Marshal(api.OrchSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Prompt: "x", Node: "zzz"})
	_, err := d.handleOrchSessionSpawn(t.Context(), params)
	if err == nil || err.Error() != "unknown node: zzz" {
		t.Fatalf("err = %v", err)
	}
	if fs.gotCwd != "" {
		t.Fatal("spawner was called")
	}
}

func TestSpawnOwnNodeByIDAndLabel(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	d.label = "Home Box"
	for _, node := range []string{"home", "Home Box"} {
		params, _ := json.Marshal(api.OrchSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Node: node})
		out, err := d.handleOrchSessionSpawn(t.Context(), params)
		if err != nil {
			t.Fatalf("%s: %v", node, err)
		}
		if res := out.(api.OrchSpawnResult); res.SessionID != "home:fake:ses_x" || res.ChildKey != "" {
			t.Fatalf("%s: result = %+v", node, res)
		}
	}
}

func TestSpawnRemoteRequestIDDedupe(t *testing.T) {
	sp := newSpawnPeers(t, "")
	first, err := sp.spawn(t, "b", "r1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := sp.spawn(t, "b", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
	if err := sp.a.orch.SetEndedBy(t.Context(), api.Actor{Node: "b", Key: "fake:ses_x"}, "self"); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.spawn(t, "b", "r1"); err != nil {
		t.Fatal(err)
	}
	if mirror, ok, _ := sp.a.orch.Link(t.Context(), api.Actor{Node: "b", Key: "fake:ses_x"}); !ok || mirror.EndedBy != "self" {
		t.Fatalf("retry rewrote the mirror: %+v ok=%v", mirror, ok)
	}
	links, err := sp.b.orch.Links(t.Context())
	if err != nil || len(links) != 1 {
		t.Fatalf("b links = %+v err=%v", links, err)
	}
	msgs, err := sp.b.orch.List(t.Context(), 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("b messages = %+v err=%v", msgs, err)
	}
}

func TestSpawnFromPeerNeverForwards(t *testing.T) {
	sp := newSpawnPeers(t, "")
	params, _ := json.Marshal(api.OrchSpawnParams{
		Caller: api.CallerInfo{Remote: &api.RemoteCaller{Actor: api.Actor{Node: "b", Key: "opencode:x"}}},
		Agent:  "fake", Cwd: t.TempDir(), Node: "b",
	})
	_, err := sp.a.handleOrchSessionSpawn(withPeerNode(peerCtx(t), "b"), params)
	if err == nil || !strings.Contains(err.Error(), "node a") {
		t.Fatalf("err = %v", err)
	}
	if sp.fs.gotCwd != "" {
		t.Fatal("b spawned")
	}
}
