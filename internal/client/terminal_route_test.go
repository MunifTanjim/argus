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
		if x.WorkspaceID != "" {
			t.Errorf("terminal %s workspace = %q, want empty", x.ID, x.WorkspaceID)
		}
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
	if err := c.Call(api.MethodTerminalInput, api.TerminalInputParams{TermID: "t1", Data: []byte("x")}, nil); err != nil {
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
			b, _ := json.Marshal(api.Terminal{ID: "@5", Cwd: "~", Command: "zsh", WorkspaceID: "w1"})
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
	if got.ID != session.CompositeID("n1", "@5") || got.WorkspaceID != session.CompositeID("n1", "w1") || got.NodeID != "n1" || got.NodeLabel != "n1-box" {
		t.Errorf("created = %+v, want composite ids and node origin", got)
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

func newCBORNode(t *testing.T, id string, cbor bool) (*fakeNode, *[]api.TerminalInputParams) {
	t.Helper()
	k, _ := e2e.GenerateKeyPair()
	var inputs []api.TerminalInputParams
	n := &fakeNode{id: id, key: k, cbor: cbor, handle: func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodTerminalInput {
			var p api.TerminalInputParams
			_ = json.Unmarshal(params, &p)
			inputs = append(inputs, p)
		}
		return json.RawMessage(`null`), nil, nil
	}}
	return n, &inputs
}

// sendTerminalInput opens t1 on n1 and sends terminal.input with bytes that are
// not valid UTF-8.
func sendTerminalInput(t *testing.T, c *E2EClient) {
	t.Helper()
	if err := c.Call(api.MethodTerminalOpen, api.TerminalOpenParams{TermID: "t1", TerminalID: session.CompositeID("n1", "@3"), Cols: 80, Rows: 24}, nil); err != nil {
		t.Fatalf("terminal.open: %v", err)
	}
	if err := c.Call(api.MethodTerminalInput, api.TerminalInputParams{TermID: "t1", Data: []byte{0x00, 0xff, 'x'}}, nil); err != nil {
		t.Fatalf("terminal.input: %v", err)
	}
}

func lastEnc(encs []api.Encoding) api.Encoding {
	if len(encs) == 0 {
		return api.Encoding(255)
	}
	return encs[len(encs)-1]
}

func TestE2EClientTerminalInputCBOR(t *testing.T) {
	n, inputs := newCBORNode(t, "n1", true)
	g, conn := newFakeMultiGateway(t, n)
	g.cbor = true
	defer g.peer.Close()
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	sendTerminalInput(t, c)
	if got := g.requestEncs(n, api.MethodTerminalInput); len(got) != 1 || got[0] != api.EncodingCBOR {
		t.Fatalf("terminal.input encodings = %v, want [CBOR]", got)
	}
	if got := g.requestEncs(n, api.MethodTerminalOpen); lastEnc(got) != api.EncodingJSON {
		t.Errorf("terminal.open encodings = %v, want JSON", got)
	}
	if len(*inputs) != 1 || !bytes.Equal((*inputs)[0].Data, []byte{0x00, 0xff, 'x'}) || (*inputs)[0].TermID != "t1" {
		t.Errorf("node got %+v, want t1 with raw bytes", *inputs)
	}
	g.mu.Lock()
	idp := n.identifyParams
	g.mu.Unlock()
	if len(idp) == 0 || string(idp[0]) != `{"capabilities":{"cbor":true}}` {
		t.Errorf("identify params = %s, want capabilities.cbor true", idp)
	}
}

func TestE2EClientJSONBehindOldGateway(t *testing.T) {
	n, inputs := newCBORNode(t, "n1", true)
	g, conn := newFakeMultiGateway(t, n)
	defer g.peer.Close()
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	sendTerminalInput(t, c)
	if got := g.requestEncs(n, api.MethodTerminalInput); len(got) != 1 || got[0] != api.EncodingJSON {
		t.Fatalf("terminal.input encodings = %v, want [JSON]", got)
	}
	if len(*inputs) != 1 || !bytes.Equal((*inputs)[0].Data, []byte{0x00, 0xff, 'x'}) {
		t.Errorf("node got %+v, want raw bytes", *inputs)
	}
}

func TestE2EClientJSONToOldNode(t *testing.T) {
	n, _ := newCBORNode(t, "n1", false)
	g, conn := newFakeMultiGateway(t, n)
	g.cbor = true
	defer g.peer.Close()
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	sendTerminalInput(t, c)
	if got := g.requestEncs(n, api.MethodTerminalInput); len(got) != 1 || got[0] != api.EncodingJSON {
		t.Fatalf("terminal.input encodings = %v, want [JSON]", got)
	}
}

func TestChannelCBORRecomputedOnReopen(t *testing.T) {
	n, _ := newCBORNode(t, "n1", true)
	g, conn := newFakeMultiGateway(t, n)
	g.cbor = true
	defer g.peer.Close()
	c, _ := NewE2EClient(conn)
	c.offlineGrace = time.Hour
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	sendTerminalInput(t, c)
	if got := lastEnc(g.requestEncs(n, api.MethodTerminalInput)); got != api.EncodingCBOR {
		t.Fatalf("first channel terminal.input = %v, want CBOR", got)
	}

	g.emitNodeEvent(api.NodeEventOffline, n)
	deadline := time.Now().Add(2 * time.Second)
	for c.byNodeSnapshot()["n1"] != nil {
		if time.Now().After(deadline) {
			t.Fatal("channel never dropped")
		}
		time.Sleep(5 * time.Millisecond)
	}
	g.setNodeCBOR(n, false)
	g.emitNodeEvent(api.NodeEventOnline, n)
	for len(g.requestEncs(n, api.MethodNodeIdentify)) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("channel never reopened")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let the identify reply land
	sendTerminalInput(t, c)
	if got := lastEnc(g.requestEncs(n, api.MethodTerminalInput)); got != api.EncodingJSON {
		t.Fatalf("reopened channel terminal.input = %v, want JSON", got)
	}
}

func TestE2EClientCBOROutputNotification(t *testing.T) {
	raw := []byte{0x1b, '[', 'A', 0xff}
	out, _ := api.CBORMarshal(api.TerminalOutput{TermID: "t1", Data: raw})
	k, _ := e2e.GenerateKeyPair()
	n := &fakeNode{id: "n1", key: k, cbor: true, handle: func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return json.RawMessage(`null`), nil, &fakeNote{method: api.MethodTerminalOutput, params: out, enc: api.EncodingCBOR}
	}}
	g, conn := newFakeMultiGateway(t, n)
	g.cbor = true
	defer g.peer.Close()
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
		if ev.Method != api.MethodTerminalOutput || ev.Enc != api.EncodingCBOR {
			t.Fatalf("notification = %s enc %v, want CBOR terminal.output", ev.Method, ev.Enc)
		}
		var o api.TerminalOutput
		if err := ev.Decode(&o); err != nil {
			t.Fatal(err)
		}
		if o.TermID != "t1" || !bytes.Equal(o.Data, raw) {
			t.Errorf("decoded = %+v, want t1 with raw bytes", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal.output never reached Events()")
	}
}

func TestE2EClientStampsCBORNonTerminalNotification(t *testing.T) {
	empty, _ := api.CBORMarshal(map[string]any{})
	k, _ := e2e.GenerateKeyPair()
	n := &fakeNode{id: "n1", key: k, cbor: true, handle: func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return json.RawMessage(`null`), nil, &fakeNote{method: api.MethodTerminalChanged, params: empty, enc: api.EncodingCBOR}
	}}
	g, conn := newFakeMultiGateway(t, n)
	g.cbor = true
	defer g.peer.Close()
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
		if ev.Method != api.MethodTerminalChanged || ev.Enc != api.EncodingJSON || string(ev.Params) != `{"node_id":"n1"}` {
			t.Fatalf("notification = %s enc %v %s, want stamped JSON terminal.changed", ev.Method, ev.Enc, ev.Params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal.changed never reached Events()")
	}
}

func TestHandleTermIDSkipsMarshalForTypedInput(t *testing.T) {
	var typed any = api.TerminalInputParams{TermID: "t1", Data: make([]byte, 4096)}
	for _, tc := range []struct {
		params any
		want   string
	}{
		{typed, "t1"},
		{api.TerminalResizeParams{TermID: "t3", Cols: 80, Rows: 24}, "t3"},
		{json.RawMessage(`{"term_id":"t4"}`), "t4"},
		{nil, ""},
	} {
		if got, err := handleTermID(tc.params); err != nil || got != tc.want {
			t.Errorf("handleTermID(%T) = %q, %v; want %q", tc.params, got, err, tc.want)
		}
	}
	if n := testing.AllocsPerRun(100, func() { _, _ = handleTermID(typed) }); n != 0 {
		t.Errorf("handleTermID allocs = %v, want 0 (no JSON marshal)", n)
	}
}
