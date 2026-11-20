package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/procwalk"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestPeerChannelCannotImpersonateLocal(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		url := startPeerGateway(t)
		a, _ := startPeerNode(t, url, "a", mustKeyPair(t), e2ee)
		fd := &fakeDismisser{}
		b, _ := startPeerNodeWith(t, url, "b", mustKeyPair(t), e2ee, func(d *Node) {
			sqlDB, err := db.Open(t.TempDir() + "/argus.db")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { sqlDB.Close() })
			d.SetOrchStore(orch.NewStore(sqlDB, time.Now))
			d.adapterList = adapters.All()
			d.adapters["claude"] = fd
			table := procwalk.Table{
				1:   {PID: 1, PPID: 0, Args: "/sbin/launchd"},
				100: {PID: 100, PPID: 1, Args: "/usr/local/bin/claude"},
				120: {PID: 120, PPID: 100, Args: "argus session kill"},
			}
			d.procTable = func(context.Context) (procwalk.Table, error) { return table, nil }
			d.addAgentSession("claude", "p1")
			d.addAgentSession("claude", "p2")
			if err := d.orch.PutLink(context.Background(), api.Link{
				Child:  api.Actor{Node: "b", Key: "claude:p2"},
				Parent: api.Actor{Node: "b", Key: "claude:p1"},
			}); err != nil {
				t.Fatal(err)
			}
		})
		waitPeerOnline(t, a, "b", e2ee)
		waitPeerOnline(t, b, "a", e2ee)

		caller := api.CallerInfo{PID: 120, Env: map[string]string{"CLAUDE_PID": "100", "CLAUDE_CODE_SESSION_ID": "p1"}}
		if s, err := b.callerFrom(t.Context(), caller); err != nil || s.Local == nil || s.Local.AgentSessionID != "p1" {
			t.Fatalf("caller does not resolve locally on b: %+v %v", s, err)
		}
		err := a.peers.Load().call(peerCtx(t), "b", api.MethodOrchSessionKill,
			api.OrchTargetParams{Caller: caller, SessionID: "b:claude:p2"}, nil)
		if err == nil || !strings.Contains(err.Error(), errLocalCallerOnPeer.Error()) {
			t.Fatalf("kill over a peer channel with a local caller: err = %v", err)
		}
		if fd.called {
			t.Fatal("target was killed")
		}
		if _, ok := b.reg.FindByAgentSession("claude", "p2"); !ok {
			t.Fatal("target session is gone")
		}
	})
}

func remoteCaller(node, key string) api.CallerInfo {
	return api.CallerInfo{Remote: &api.RemoteCaller{
		Actor: api.Actor{Node: node, Key: key}, Label: "worker",
	}}
}

func TestCallerFromRemoteOnPeerChannel(t *testing.T) {
	d := callerNode()
	d.SetIdentity("home", "")
	ref, err := d.callerFrom(withPeerNode(t.Context(), "b"), remoteCaller("b", "claude:k"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Actor != (api.Actor{Node: "b", Key: "claude:k"}) || ref.Label != "worker" || ref.Local != nil {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestCallerFromRemoteNodeMismatch(t *testing.T) {
	d := callerNode()
	_, err := d.callerFrom(withPeerNode(t.Context(), "b"), remoteCaller("c", "claude:k"))
	if !errors.Is(err, errRemoteCallerDenied) {
		t.Fatalf("err = %v", err)
	}
}

func TestCallerFromRemoteOnLocalSocket(t *testing.T) {
	d := callerNode()
	_, err := d.callerFrom(t.Context(), remoteCaller("b", "claude:k"))
	if !errors.Is(err, errRemoteCallerDenied) {
		t.Fatalf("err = %v", err)
	}
}

func TestCallerFromLocalPIDOnPeerChannel(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	ref, err := d.callerFrom(t.Context(), caller)
	if err != nil || ref.Local == nil || ref.Actor != (api.Actor{Node: "home", Key: "claude:p1"}) {
		t.Fatalf("local ref = %+v err=%v", ref, err)
	}
	if _, err := d.callerFrom(withPeerNode(t.Context(), "b"), caller); !errors.Is(err, errLocalCallerOnPeer) {
		t.Fatalf("err = %v", err)
	}
}

func TestReplyChecksActorNode(t *testing.T) {
	d, _ := orchNode(t)
	d.SetIdentity("home", "")
	if _, err := d.orch.Insert(t.Context(), api.Message{
		ID: "home:m-00000001", Kind: api.MessageText, State: api.MessageDelivered,
		From: api.Actor{Node: "home", Key: api.ActorOwner}, To: api.Actor{Node: "b", Key: "claude:k"}, Body: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	ref, err := d.callerFrom(withPeerNode(t.Context(), "c"), remoteCaller("c", "claude:k"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.reply(t.Context(), ref, "home:m-00000001", "x")
	if err == nil || !strings.Contains(err.Error(), "only the recipient") {
		t.Fatalf("err = %v", err)
	}
}

func TestKillChecksActorNode(t *testing.T) {
	d, fd, _ := killNode(t)
	ref, err := d.callerFrom(withPeerNode(t.Context(), "c"), remoteCaller("c", "claude:p1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.kill(t.Context(), ref, "home:claude:p2"); err == nil || !strings.Contains(err.Error(), "only the parent") {
		t.Fatalf("err = %v", err)
	}
	if fd.called {
		t.Fatal("target was killed")
	}
}

func TestRemoteCallerDecodesActor(t *testing.T) {
	d := callerNode()
	d.SetIdentity("home", "")
	c, err := api.Decode[api.CallerInfo](json.RawMessage(`{"remote":{"actor":{"node":"b","key":"claude:x"},"session_id":"b:argus:%1","label":"w"}}`))
	if err != nil || c.Remote == nil || c.Remote.Actor != (api.Actor{Node: "b", Key: "claude:x"}) {
		t.Fatalf("caller = %+v err=%v", c, err)
	}
	c.Remote.Actor.Key = api.ActorOwner
	if _, err := d.callerFrom(withPeerNode(t.Context(), "b"), c); !errors.Is(err, errRemoteCallerDenied) {
		t.Fatalf("reserved key: err = %v, want errRemoteCallerDenied", err)
	}
}

func TestCurrentSessionErrorNamesNoOldTool(t *testing.T) {
	d := callerNode()
	_, err := d.currentSession(callerRef{Actor: api.Actor{Node: "b", Key: "claude:x"}})
	if err == nil || strings.Contains(err.Error(), "whoami") {
		t.Fatalf("err = %v, want an error that does not name whoami", err)
	}
}

func TestRemoteCallerWithoutKeyNamedByLabel(t *testing.T) {
	d, _ := deliverNode(t)
	b := apiSession(d, "b", session.StatusIdle, nil)
	caller := callerRef{Actor: api.Actor{Node: "far"}, Label: "worker"}
	_, err := d.send(t.Context(), caller, d.agentID(b), "hi", "")
	if err == nil || !strings.HasPrefix(err.Error(), "worker is not ready yet") {
		t.Fatalf("err = %v", err)
	}
}
