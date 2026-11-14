package node

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

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
	params, _ := json.Marshal(api.OrchTargetParams{Caller: caller, SessionID: "home:default:%" + id})
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
	want := []string{"home:default:%p4", "home:default:%p3", "home:default:%p5", "home:default:%p2"}
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
	want := []string{"home:default:%p4", "home:default:%p3", "home:default:%p7", "home:default:%p5", "home:default:%p2"}
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
	if err == nil || !strings.Contains(err.Error(), "kill home:default:%p3: boom") ||
		!strings.Contains(err.Error(), "killed: home:default:%p4, home:default:%p5, home:default:%p2") {
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
