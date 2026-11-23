package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

// fleetBots is a fake node that sees "scout" on two nodes and "solo" on one.
func fleetBots() *fakeBotNode {
	return &fakeBotNode{bots: []api.BotInfo{
		{ID: "a1", Name: "scout", NodeID: "n1", NodeLabel: "laptop", Status: "idle"},
		{ID: "a2", Name: "scout", NodeID: "n2", NodeLabel: "server", Status: "idle"},
		{ID: "a3", Name: "solo", NodeID: "n2", NodeLabel: "server", Status: "idle"},
	}}
}

func TestNodeParamsAddNodeField(t *testing.T) {
	b, err := json.Marshal(nodeParams{Node: "n2", Params: api.BotRef{BotID: "a2"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"bot_id":"a2","node":"n2"}` {
		t.Fatalf("json = %s", b)
	}
	if b, _ := json.Marshal(nodeParams{Node: "n2"}); string(b) != `{"node":"n2"}` {
		t.Fatalf("nil params json = %s", b)
	}
}

func TestBotListReachesAllNodes(t *testing.T) {
	f := fleetBots()
	f.unreachable = []string{"n3"}
	out, errOut, err := execBot(t, f, "list")
	if err != nil {
		t.Fatal(err)
	}
	if p := f.params[api.MethodBotList][0].(api.BotListParams); !p.All {
		t.Fatalf("list params = %+v", p)
	}
	if !strings.Contains(out, "NODE") || !strings.Contains(out, "laptop") || !strings.Contains(out, "server") {
		t.Fatalf("list:\n%s", out)
	}
	if !strings.Contains(errOut, "unreachable nodes, their bots are not listed: n3") {
		t.Fatalf("stderr = %q", errOut)
	}

	f = fleetBots()
	if _, _, err := execBot(t, f, "list", "--node", "server"); err != nil {
		t.Fatal(err)
	}
	p := f.params[api.MethodBotList][0].(nodeParams)
	if p.Node != "server" || p.Params.(api.BotListParams).All {
		t.Fatalf("list --node params = %+v", p)
	}
}

func TestBotCommandsRunOnTheBotsNode(t *testing.T) {
	for _, args := range [][]string{
		{"show", "scout@server"},
		{"show", "scout", "--node", "server"},
		{"show", "scout@n2"},
		{"show", "solo"},
	} {
		f := fleetBots()
		if _, errOut, err := execBot(t, f, args...); err != nil {
			t.Fatalf("%v: %v %s", args, err, errOut)
		}
		got := f.params[api.MethodBotShow][0].(nodeParams)
		if got.Node != "n2" {
			t.Fatalf("%v: params = %+v", args, got)
		}
	}

	f := fleetBots()
	if _, errOut, err := execBot(t, f, "show", "scout@laptop", "--node", "server"); err == nil || !strings.Contains(errOut, "--node is server") {
		t.Fatalf("conflicting node: %v %q", err, errOut)
	}
	f.unreachable = []string{"n3"}
	if _, errOut, err := execBot(t, f, "show", "far"); err == nil || !strings.Contains(errOut, "unreachable nodes: n3") {
		t.Fatalf("unknown with unreachable: %v %q", err, errOut)
	}

	// chat continues on the bot's node, through to the wait.
	f = fleetBots()
	f.threads = []api.BotThreadsResult{{}}
	if _, errOut, err := execBot(t, f, "chat", "scout@laptop", "hi"); err != nil {
		t.Fatalf("chat: %v %s", err, errOut)
	}
	if got := f.params[api.MethodBotThreadSend][0].(nodeParams); got.Node != "n1" || got.Params.(api.BotSendParams).BotID != "a1" {
		t.Fatalf("chat params = %+v", got)
	}

	// primary --unset clears the primary of --node.
	f = fleetBots()
	if _, _, err := execBot(t, f, "primary", "--unset", "--node", "server"); err != nil {
		t.Fatal(err)
	}
	if got := f.params[api.MethodBotSetPrimary][0].(nodeParams); got.Node != "server" {
		t.Fatalf("primary params = %+v", got)
	}
}

func TestBotMemoryRunsOnTheBotsNode(t *testing.T) {
	m := &memoryNode{fakeBotNode: fleetBots()}
	if _, err := execMemory(t, m, "log", "scout@server"); err != nil {
		t.Fatal(err)
	}
	got := m.params[api.MethodBotMemoryLog][0].(nodeParams)
	if got.Node != "n2" || got.Params.(api.BotMemoryLogParams).BotID != "a2" {
		t.Fatalf("memory log params = %+v", got)
	}
	if _, err := execMemory(t, m, "review", "scout", "--node", "laptop", "--accept", "p1"); err != nil {
		t.Fatal(err)
	}
	if got := m.params[api.MethodBotMemoryDecide][0].(nodeParams); got.Node != "n1" {
		t.Fatalf("memory decide params = %+v", got)
	}
}

func TestRoutineRunsOnTheBotsNode(t *testing.T) {
	newNode := func() *fakeRoutineNode {
		return &fakeRoutineNode{bots: fleetBots().bots}
	}
	for _, args := range [][]string{
		{"run", "scout@server/inbox"},
		{"run", "scout/inbox", "--node", "server"},
	} {
		f := newNode()
		if _, errOut, err := execRoutineErr(t, f, args...); err != nil {
			t.Fatalf("%v: %v %s", args, err, errOut)
		}
		got := f.params[api.MethodBotRoutineRun].(nodeParams)
		if r := got.Params.(api.BotRoutineRef); got.Node != "n2" || r.BotID != "a2" || r.Slug != "inbox" {
			t.Fatalf("%v: params = %+v", args, got)
		}
	}
	f := newNode()
	if _, errOut, err := execRoutineErr(t, f, "run", "scout/inbox"); err == nil || !strings.Contains(errOut, "several nodes") {
		t.Fatalf("ambiguous: %v %q", err, errOut)
	}

	out, err := execRoutine(t, newNode(), "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NODE", "scout/inbox", "solo/inbox", "laptop", "server"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
}
