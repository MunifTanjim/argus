package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/procwalk"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestLastAssistantText(t *testing.T) {
	e := func(k transcript.EntryKind, text string) transcript.Entry {
		return transcript.Entry{Kind: k, Text: text}
	}
	cases := []struct {
		name    string
		entries []transcript.Entry
		want    string
	}{
		{"after last user", []transcript.Entry{
			e(transcript.EntryUser, "q1"), e(transcript.EntryText, "a1"), e(transcript.EntryUser, "q2"),
			e(transcript.EntryTool, ""), e(transcript.EntryText, "a2"), e(transcript.EntryText, "a3"),
		}, "a2\n\na3"},
		{"no user entry", []transcript.Entry{e(transcript.EntryText, "a"), e(transcript.EntryText, "b")}, "a\n\nb"},
		{"no text", []transcript.Entry{e(transcript.EntryUser, "q")}, ""},
	}
	for _, c := range cases {
		if got := lastAssistantText(c.entries); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func homeNode() *Node {
	d := callerNode()
	d.id = "home"
	d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerArgus, PaneID: "%3", Status: session.StatusIdle,
	})
	return d
}

func TestAgentListComposites(t *testing.T) {
	d := homeNode()
	out, err := d.handleAgentList(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	list := out.([]session.Session)
	if len(list) != 1 || list[0].ID != "home:argus:%3" {
		t.Fatalf("got %+v", list)
	}
}

func TestAgentWhoamiNeedsCaller(t *testing.T) {
	d := homeNode()
	params, _ := json.Marshal(api.CallerParams{Caller: api.CallerInfo{PID: 5}})
	_, err := d.handleAgentWhoami(t.Context(), params)
	if !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentShowDisabledWithoutMesh(t *testing.T) {
	d := homeNode()
	params, _ := json.Marshal(api.AgentTargetParams{SessionID: "home:argus:%3"})
	_, err := d.handleAgentShow(t.Context(), params)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalTargetForeignNode(t *testing.T) {
	d := homeNode()
	_, err := d.localTarget("other:argus:%3")
	if err == nil || !strings.Contains(err.Error(), "cross-machine") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalTargetOwnPrefixAndBare(t *testing.T) {
	d := homeNode()
	for _, id := range []string{"home:argus:%3", "argus:%3"} {
		s, err := d.localTarget(id)
		if err != nil || s.ID != "argus:%3" {
			t.Errorf("%s: s=%q err=%v", id, s.ID, err)
		}
	}
}

func TestAgentShowReturnsAllQueued(t *testing.T) {
	d, store := meshNode(t)
	d.id = "home"
	hookSession(d, "%3", "c1")
	for range 2 {
		if _, err := store.Insert(t.Context(), api.Message{
			Kind: api.MessageText, From: api.Party{Node: "home", Key: api.PartyOwner},
			To: api.Party{Node: "home", Key: "claude:c1"}, Body: "hi", State: api.MessageQueued,
		}); err != nil {
			t.Fatal(err)
		}
	}
	params, _ := json.Marshal(api.AgentTargetParams{SessionID: "home:default:%3"})
	out, err := d.handleAgentShow(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(api.AgentShowResult)
	if len(res.Queued) != 2 || res.Session.ID != "home:default:%3" {
		t.Fatalf("got %+v", res)
	}
}

func spawnNode(t *testing.T, withCaller bool) (*Node, *fakeSpawner, api.CallerInfo) {
	t.Helper()
	d, _ := meshNode(t)
	d.id = "home"
	fs := &fakeSpawner{id: "ses_x"}
	d.adapters["fake"] = fs
	d.adapterList = adapters.All()
	args := "zsh"
	if withCaller {
		args = "/usr/local/bin/claude"
	}
	table := procwalk.Table{
		1:   {PID: 1, PPID: 0, Args: "/sbin/launchd"},
		100: {PID: 100, PPID: 1, Args: args},
		120: {PID: 120, PPID: 100, Args: "argus session spawn"},
	}
	d.procTable = func(context.Context) (procwalk.Table, error) { return table, nil }
	d.addAgentSession("claude", "p1")
	env := map[string]string{}
	if withCaller {
		env = map[string]string{"CLAUDE_PID": "100", "CLAUDE_CODE_SESSION_ID": "p1"}
	}
	return d, fs, api.CallerInfo{PID: 120, Env: env}
}

func TestAgentSpawnLinksParentAndTask(t *testing.T) {
	d, fs, caller := spawnNode(t, true)
	params, _ := json.Marshal(api.AgentSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Prompt: "build it"})
	out, err := d.handleAgentSpawn(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(api.AgentSpawnResult)
	if res.SessionID != "home:fake:ses_x" || !strings.HasPrefix(res.TaskID, "m-") {
		t.Fatalf("got %+v", res)
	}
	if !strings.Contains(fs.gotPrompt, "build it") || !strings.Contains(fs.gotPrompt, "argus session reply "+res.TaskID) {
		t.Fatalf("prompt = %q", fs.gotPrompt)
	}
	link, ok, err := d.mesh.Link(t.Context(), "fake:ses_x")
	if err != nil || !ok || link.Parent.Key != "claude:p1" || link.TaskID != res.TaskID {
		t.Fatalf("link = %+v ok=%v err=%v", link, ok, err)
	}
	m, ok, err := d.mesh.Get(t.Context(), res.TaskID)
	if err != nil || !ok || m.State != api.MessageDelivered || m.To.Key != "fake:ses_x" || m.Kind != api.MessageTask {
		t.Fatalf("message = %+v ok=%v err=%v", m, ok, err)
	}
}

func TestAgentSpawnWithoutPromptHasNoTask(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	params, _ := json.Marshal(api.AgentSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir()})
	out, err := d.handleAgentSpawn(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(api.AgentSpawnResult); res.TaskID != "" {
		t.Fatalf("task id = %q", res.TaskID)
	}
	link, ok, err := d.mesh.Link(t.Context(), "fake:ses_x")
	if err != nil || !ok || link.TaskID != "" {
		t.Fatalf("link = %+v ok=%v err=%v", link, ok, err)
	}
}

func TestAgentSpawnNeedsCaller(t *testing.T) {
	d, fs, caller := spawnNode(t, false)
	params, _ := json.Marshal(api.AgentSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Prompt: "x"})
	_, err := d.handleAgentSpawn(t.Context(), params)
	if !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("err = %v", err)
	}
	if fs.gotCwd != "" {
		t.Fatal("spawner was called")
	}
}

func TestAgentSpawnFailureLeavesNoLink(t *testing.T) {
	d, fs, caller := spawnNode(t, true)
	fs.err = errors.New("boom")
	params, _ := json.Marshal(api.AgentSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Prompt: "x"})
	if _, err := d.handleAgentSpawn(t.Context(), params); err == nil {
		t.Fatal("expected error")
	}
	links, err := d.mesh.Links(t.Context())
	if err != nil || len(links) != 0 {
		t.Fatalf("links = %+v err=%v", links, err)
	}
}

func TestAgentSpawnRekeysChildRegisteredBeforeLink(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	d.reg.ApplyHook(registry.HookUpdate{
		Agent: "fake", Server: session.TmuxServer("fake"), PaneID: "ses_x",
		AgentSessionID: "real", Status: session.StatusIdle,
	})
	d.meshPass(t.Context())
	params, _ := json.Marshal(api.AgentSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Prompt: "go"})
	out, err := d.handleAgentSpawn(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	taskID := out.(api.AgentSpawnResult).TaskID
	link, ok, err := d.mesh.Link(t.Context(), "fake:real")
	if err != nil || !ok || link.TaskID != taskID {
		t.Fatalf("link = %+v ok=%v err=%v", link, ok, err)
	}
	if _, ok, _ := d.mesh.Link(t.Context(), "fake:ses_x"); ok {
		t.Fatal("link left under the old key")
	}
	m, ok, err := d.mesh.Get(t.Context(), taskID)
	if err != nil || !ok || m.To.Key != "fake:real" {
		t.Fatalf("message = %+v ok=%v err=%v", m, ok, err)
	}
}

func TestAgentSpawnRejectsUnorchestratedAgent(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	params, _ := json.Marshal(api.AgentSpawnParams{Caller: caller, Agent: "antigravity", Cwd: t.TempDir(), Prompt: "go"})
	_, err := d.handleAgentSpawn(t.Context(), params)
	if err == nil || err.Error() != "antigravity does not support agent sessions" {
		t.Fatalf("err = %v", err)
	}
	links, err := d.mesh.Links(t.Context())
	if err != nil || len(links) != 0 {
		t.Fatalf("links = %+v err=%v", links, err)
	}
}
