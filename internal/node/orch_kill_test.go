package node

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

type recordingDismisser struct {
	adapter.Adapter
	dismissed []string
	fail      map[string]bool
}

func (r *recordingDismisser) Dismiss(_ context.Context, s session.Session) error {
	if r.fail[s.AgentSessionID] {
		return errors.New("boom")
	}
	r.dismissed = append(r.dismissed, s.AgentSessionID)
	return nil
}

func (r *recordingDismisser) IsHeadless() bool { return true }

// treeNode builds p1 -> p2 -> {p3 -> p4, p5}, all claude sessions; p1 is the caller.
func treeNode(t *testing.T) (*Node, *recordingDismisser, api.CallerInfo) {
	t.Helper()
	d, _, caller := spawnNode(t, true)
	rd := &recordingDismisser{fail: map[string]bool{}}
	d.adapters["claude"] = rd
	for _, l := range [][2]string{{"p2", "p1"}, {"p3", "p2"}, {"p4", "p3"}, {"p5", "p2"}} {
		d.addAgentSession("claude", l[0])
		if err := d.orch.PutLink(t.Context(), api.Link{
			Child:  api.Actor{Node: "home", Key: "claude:" + l[0]},
			Parent: api.Actor{Node: "home", Key: "claude:" + l[1]},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return d, rd, caller
}

func endedBy(t *testing.T, d *Node, id string) string {
	t.Helper()
	link, ok, err := d.orch.Link(t.Context(), api.Actor{Node: d.id, Key: "claude:" + id})
	if err != nil || !ok {
		t.Fatalf("link %s: ok=%v err=%v", id, ok, err)
	}
	return link.EndedBy
}

func agentKill(t *testing.T, d *Node, caller api.CallerInfo, id string) (api.OrchKillResult, error) {
	t.Helper()
	params, _ := json.Marshal(api.OrchTargetParams{Caller: caller, SessionID: "home:claude:" + id})
	out, err := d.handleOrchSessionKill(t.Context(), params)
	if err != nil {
		return api.OrchKillResult{}, err
	}
	return out.(api.OrchKillResult), nil
}

func TestAgentKillCascadesDeepestFirst(t *testing.T) {
	d, rd, caller := treeNode(t)
	res, err := agentKill(t, d, caller, "p2")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"home:claude:p4", "home:claude:p3", "home:claude:p5", "home:claude:p2"}
	if !slices.Equal(res.Killed, want) {
		t.Fatalf("killed = %v, want %v", res.Killed, want)
	}
	if !slices.Equal(rd.dismissed, []string{"p4", "p3", "p5", "p2"}) {
		t.Fatalf("dismissed = %v", rd.dismissed)
	}
	for _, id := range []string{"p3", "p4", "p5"} {
		if by := endedBy(t, d, id); by != "ancestor" {
			t.Errorf("%s ended_by = %q, want ancestor", id, by)
		}
	}
	if by := endedBy(t, d, "p2"); by != "parent" {
		t.Errorf("p2 ended_by = %q, want parent", by)
	}
}

func TestOwnerKillCascades(t *testing.T) {
	d, rd, _ := treeNode(t)
	params, _ := json.Marshal(api.SessionRef{SessionID: "default:%p3"})
	if _, err := d.handleSessionKill(t.Context(), params); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rd.dismissed, []string{"p4", "p3"}) {
		t.Fatalf("dismissed = %v", rd.dismissed)
	}
	if by := endedBy(t, d, "p4"); by != "ancestor" {
		t.Errorf("p4 ended_by = %q", by)
	}
	if by := endedBy(t, d, "p3"); by != "owner" {
		t.Errorf("p3 ended_by = %q", by)
	}
}

func TestKillReachesBelowAGoneChild(t *testing.T) {
	d, rd, caller := treeNode(t)
	d.addAgentSession("claude", "p7")
	for _, l := range [][2]string{{"p6", "p5"}, {"p7", "p6"}} {
		if err := d.orch.PutLink(t.Context(), api.Link{
			Child:  api.Actor{Node: "home", Key: "claude:" + l[0]},
			Parent: api.Actor{Node: "home", Key: "claude:" + l[1]},
		}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := agentKill(t, d, caller, "p2")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"home:claude:p4", "home:claude:p3", "home:claude:p7", "home:claude:p5", "home:claude:p2"}
	if !slices.Equal(res.Killed, want) {
		t.Fatalf("killed = %v, want %v", res.Killed, want)
	}
	if !slices.Equal(rd.dismissed, []string{"p4", "p3", "p7", "p5", "p2"}) {
		t.Fatalf("dismissed = %v", rd.dismissed)
	}
}

func TestKillContinuesAfterAFailedDescendant(t *testing.T) {
	d, rd, caller := treeNode(t)
	rd.fail["p3"] = true
	_, err := agentKill(t, d, caller, "p2")
	if err == nil || !strings.Contains(err.Error(), "kill home:claude:p3: boom") ||
		!strings.Contains(err.Error(), "killed: home:claude:p4, home:claude:p5, home:claude:p2") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rd.dismissed, []string{"p4", "p5", "p2"}) {
		t.Fatalf("dismissed = %v", rd.dismissed)
	}
	if by := endedBy(t, d, "p3"); by != "" {
		t.Errorf("p3 ended_by = %q, want empty after its kill failed", by)
	}
}

func TestDescendantCountMatchesWhatAKillKills(t *testing.T) {
	d, _, _ := treeNode(t)
	d.addAgentSession("claude", "p7")
	for _, l := range [][2]string{{"p6", "p5"}, {"p7", "p6"}} {
		if err := d.orch.PutLink(t.Context(), api.Link{
			Child:  api.Actor{Node: "home", Key: "claude:" + l[0]},
			Parent: api.Actor{Node: "home", Key: "claude:" + l[1]},
		}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(id string) int {
		params, _ := json.Marshal(api.SessionRef{SessionID: "default:%" + id})
		out, err := d.handleSessionDescendantCount(t.Context(), params)
		if err != nil {
			t.Fatal(err)
		}
		return out.(api.DescendantCountResult).Count
	}
	if n := count("p2"); n != 4 {
		t.Errorf("p2 count = %d, want 4 (p3, p4, p5, and p7 below the gone p6)", n)
	}
	if n := count("p4"); n != 0 {
		t.Errorf("p4 count = %d, want 0", n)
	}
	params, _ := json.Marshal(api.SessionRef{SessionID: "default:%nope"})
	if _, err := d.handleSessionDescendantCount(t.Context(), params); err == nil {
		t.Error("an unknown session should fail")
	}
}

func pendingUnder(t *testing.T, d *Node, parent api.Actor, key string) {
	t.Helper()
	linkPendingChild(t, d, parent, key)
	if _, err := d.orch.Insert(t.Context(), api.Message{
		Kind: api.MessageText, From: api.Actor{Node: d.id, Key: api.ActorOwner},
		To: api.Actor{Node: d.id, Key: key}, Body: "queued", State: api.MessageQueued,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertDoomed(t *testing.T, d *Node, key, by string) {
	t.Helper()
	if l, _, _ := d.orch.Link(t.Context(), api.Actor{Node: d.id, Key: key}); l.EndedBy != by {
		t.Fatalf("link = %+v, want ended by %s", l, by)
	}
	if q, _ := d.orch.Queued(t.Context(), api.Actor{Node: d.id, Key: key}); len(q) != 0 {
		t.Fatalf("queued = %+v, want failed", q)
	}
}

func TestKillPendingChildKillsItWhenItStarts(t *testing.T) {
	d, fd, caller := killNode(t)
	d.markOrchSettled()
	pendingUnder(t, d, api.Actor{Node: "home", Key: "claude:p1"}, "claude:p9")
	params, _ := json.Marshal(api.OrchTargetParams{Caller: caller, SessionID: "home:claude:p9"})
	out, err := d.handleOrchSessionKill(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(api.OrchKillResult); !slices.Equal(res.Killed, []string{"home:claude:p9"}) {
		t.Fatalf("result = %+v", res)
	}
	assertDoomed(t, d, "claude:p9", "parent")
	if fd.called {
		t.Fatal("dismissed before the child started")
	}
	d.addAgentSession("claude", "p9")
	d.orchPass(t.Context())
	if !fd.called {
		t.Fatal("child not killed when it started")
	}
}

func TestKillTreeReachesPendingChild(t *testing.T) {
	d, fd, caller := killNode(t)
	d.markOrchSettled()
	pendingUnder(t, d, api.Actor{Node: "home", Key: "claude:p2"}, "claude:p9")
	params, _ := json.Marshal(api.OrchTargetParams{Caller: caller, SessionID: "home:claude:p2"})
	out, err := d.handleOrchSessionKill(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(api.OrchKillResult); !slices.Contains(res.Killed, "home:claude:p9") {
		t.Fatalf("result = %+v", res)
	}
	assertDoomed(t, d, "claude:p9", "ancestor")
	fd.called = false
	d.addAgentSession("claude", "p9")
	d.orchPass(t.Context())
	if !fd.called {
		t.Fatal("child not killed when it started")
	}
}

func TestRemoteAncestorKillReachesPendingChild(t *testing.T) {
	d, _, _ := killNode(t)
	d.markOrchSettled()
	pendingUnder(t, d, api.Actor{Node: "far", Key: "claude:pp"}, "claude:p9")
	caller := callerRef{Actor: api.Actor{Node: "far", Key: "claude:pp"}}
	res, err := d.killAs(peerCtx(t), caller, "home:claude:p9", true)
	if err != nil || !slices.Equal(res.Killed, []string{"home:claude:p9"}) {
		t.Fatalf("result = %+v err = %v", res, err)
	}
	assertDoomed(t, d, "claude:p9", "ancestor")
}

func TestKilledPendingChildThatNeverStartsIsForgotten(t *testing.T) {
	d, fd, caller := killNode(t)
	d.markOrchSettled()
	pendingUnder(t, d, api.Actor{Node: "home", Key: "claude:p1"}, "claude:p9")
	params, _ := json.Marshal(api.OrchTargetParams{Caller: caller, SessionID: "home:claude:p9"})
	if _, err := d.handleOrchSessionKill(t.Context(), params); err != nil {
		t.Fatal(err)
	}
	d.orchSpawning.add("claude:p9", time.Now().Add(-2*d.orchSpawnWindow))
	d.orchPass(t.Context())
	d.addAgentSession("claude", "p9")
	d.orchPass(t.Context())
	if fd.called {
		t.Fatal("killed a session after its spawn window ended")
	}
}

func TestDescendantCountIncludesPendingChild(t *testing.T) {
	d, _, _ := killNode(t)
	pendingUnder(t, d, api.Actor{Node: "home", Key: "claude:p1"}, "claude:p9")
	p1, _ := d.reg.FindByAgentSession("claude", "p1")
	params, _ := json.Marshal(api.SessionRef{SessionID: p1.ID})
	out, err := d.handleSessionDescendantCount(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if n := out.(api.DescendantCountResult).Count; n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}
