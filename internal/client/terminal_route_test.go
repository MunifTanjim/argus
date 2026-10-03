package client

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/session"
)

func terminalListHandler(id string) func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
	return func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodTerminalList {
			return json.RawMessage(`null`), nil, nil
		}
		b, _ := json.Marshal(api.TerminalListResult{Terminals: []api.Terminal{{ID: "@1", Command: "zsh-" + id}, {ID: "@2", Command: "vim-" + id}}})
		return b, nil, nil
	}
}

func TestFanoutTerminalsCompositesAndSorts(t *testing.T) {
	k1, _ := e2e.GenerateKeyPair()
	k2, _ := e2e.GenerateKeyPair()
	k3, _ := e2e.GenerateKeyPair()
	failing := func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "boom"}, nil
	}
	g, conn := newFakeMultiGateway(t,
		&fakeNode{id: "nb", key: k1, handle: terminalListHandler("nb")},
		&fakeNode{id: "na", key: k2, handle: terminalListHandler("na")},
		&fakeNode{id: "nc", key: k3, handle: failing},
	)
	defer g.peer.Close()
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	var res api.TerminalListResult
	if err := c.Call(api.MethodTerminalList, nil, &res); err != nil {
		t.Fatalf("terminal.list: %v", err)
	}
	var got []string
	for _, x := range res.Terminals {
		got = append(got, x.ID+" "+x.NodeLabel)
	}
	want := []string{"na:@1 na-box", "na:@2 na-box", "nb:@1 nb-box", "nb:@2 nb-box"}
	if len(got) != len(want) {
		t.Fatalf("terminals = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("terminals = %v, want %v", got, want)
		}
	}
	if len(res.FailedNodes) != 1 || res.FailedNodes[0] != "nc" {
		t.Errorf("failed nodes = %v, want [nc]", res.FailedNodes)
	}
}

func TestFanoutTerminalsFailsWhenEveryNodeFails(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodTerminalList {
			return json.RawMessage(`null`), nil, nil
		}
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "boom"}, nil
	}
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(api.MethodTerminalList, nil, &api.TerminalListResult{}); err == nil {
		t.Fatal("terminal.list succeeded, want the node's error")
	}
}

func TestTerminalOpenByTerminalIDRoutesAndRecordsTheHandle(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var openTerminalID, inputTermID string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodTerminalOpen:
			openTerminalID, _ = terminalIDFromParams(params)
		case api.MethodTerminalInput:
			inputTermID, _ = termIDFromParams(params)
		}
		return json.RawMessage(`null`), nil, nil
	}
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(api.MethodTerminalOpen, api.TerminalOpenParams{TermID: "t1", TerminalID: session.CompositeID("n1", "@3"), Cols: 80, Rows: 24}, nil); err != nil {
		t.Fatalf("terminal.open: %v", err)
	}
	if openTerminalID != "@3" {
		t.Errorf("node saw terminal_id %q, want @3", openTerminalID)
	}
	if err := c.Call(api.MethodTerminalInput, api.TerminalInputParams{TermID: "t1", Data: "eA=="}, nil); err != nil {
		t.Fatalf("terminal.input: %v", err)
	}
	if inputTermID != "t1" {
		t.Errorf("terminal.input did not reach the node that holds t1")
	}
}

func TestTerminalKillAndRenameSplitTheCompositeID(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	seen := map[string]string{}
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		seen[method], _ = terminalIDFromParams(params)
		return json.RawMessage(`null`), nil, nil
	}
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	id := session.CompositeID("n1", "@4")
	if err := c.Call(api.MethodTerminalKill, api.TerminalRef{TerminalID: id}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(api.MethodTerminalRename, api.TerminalRenameParams{TerminalID: id, Name: "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if seen[api.MethodTerminalKill] != "@4" || seen[api.MethodTerminalRename] != "@4" {
		t.Errorf("node saw %v, want @4 for both", seen)
	}
}

func TestTerminalCreateCompositesTheResult(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodTerminalCreate {
			b, _ := json.Marshal(api.Terminal{ID: "@5", Cwd: "~", Command: "zsh"})
			return b, nil, nil
		}
		return json.RawMessage(`null`), nil, nil
	}
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	var got api.Terminal
	if err := c.Call(api.MethodTerminalCreate, api.TerminalCreateParams{NodeID: "n1"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != session.CompositeID("n1", "@5") || got.NodeID != "n1" || got.NodeLabel != "n1-box" {
		t.Errorf("created = %+v, want composite id and node origin", got)
	}
}

func TestFanoutTerminalsIsQuietForOldNodes(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodTerminalList {
			return json.RawMessage(`null`), nil, nil
		}
		return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: terminal.list"}, nil
	}
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	var res api.TerminalListResult
	if err := c.Call(api.MethodTerminalList, nil, &res); err != nil || len(res.Terminals) != 0 || len(res.FailedNodes) != 0 {
		t.Fatalf("terminal.list = %+v, %v; want empty, nil", res, err)
	}
	if strings.Contains(buf.String(), "warn: terminal.list") {
		t.Errorf("log = %q, want no warning for a node without terminals", buf.String())
	}
}

func TestTerminalChangedCarriesTheNodeID(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return json.RawMessage(`{}`), nil, &fakeNote{method: api.MethodTerminalChanged, params: json.RawMessage(`{}`)}
	}
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.callNode("n1", "sessions.refresh", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-c.Events():
		if ev.Method != api.MethodTerminalChanged || string(ev.Params) != `{"node_id":"n1"}` {
			t.Fatalf("notification = %s %s, want terminal.changed with node_id n1", ev.Method, ev.Params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal.changed never reached Events()")
	}
}
