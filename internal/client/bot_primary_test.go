package client

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
)

// primaryNode is a node whose primary is bot; its SetPrimary calls are
// recorded.
func primaryNode(t *testing.T, id, bot string, calls *[]string, mu *sync.Mutex) *fakeNode {
	t.Helper()
	k, _ := e2e.GenerateKeyPair()
	return &fakeNode{id: id, key: k, handle: func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodBotList:
			b, _ := json.Marshal(api.BotListResult{Bots: []api.BotInfo{{ID: bot, Primary: true}, {ID: bot + "x"}}, Primary: bot})
			return b, nil, nil
		case api.MethodBotSetPrimary:
			var p api.BotPrimaryParams
			_ = json.Unmarshal(params, &p)
			mu.Lock()
			*calls = append(*calls, id+"="+p.BotID)
			mu.Unlock()
		}
		return json.RawMessage(`null`), nil, nil
	}}
}

// TestBotListKeepsEachNodesPrimary: each node's primary stays marked, and
// no node's primary is cleared.
func TestBotListKeepsEachNodesPrimary(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	c := connectMulti(t, primaryNode(t, "n1", "a1", &calls, &mu), primaryNode(t, "n2", "b1", &calls, &mu))
	var res api.BotListResult
	if err := c.Call(api.MethodBotList, nil, &res); err != nil {
		t.Fatal(err)
	}
	var primaries []string
	for _, a := range res.Bots {
		if a.Primary {
			primaries = append(primaries, a.ID)
		}
	}
	if !slices.Equal(primaries, []string{"n1:a1", "n2:b1"}) || len(res.Bots) != 4 || res.Primary != "" {
		t.Fatalf("primaries %v, res %+v", primaries, res)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Errorf("bot.list changed primaries: %v", calls)
	}
}

// TestBotSetPrimaryOnlyItsNode: making a bot primary calls its node only.
func TestBotSetPrimaryOnlyItsNode(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	c := connectMulti(t, primaryNode(t, "n1", "a1", &calls, &mu), primaryNode(t, "n2", "b1", &calls, &mu))
	if err := c.Call(api.MethodBotSetPrimary, api.BotPrimaryParams{BotID: "n1:a1x"}, nil); err != nil {
		t.Fatalf("set: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(calls, []string{"n1=a1x"}) {
		t.Fatalf("calls = %v", calls)
	}
}

// TestBotUnsetPrimaryByNode: unsetting names the node when there are
// several, and clears only that node's primary.
func TestBotUnsetPrimaryByNode(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	c := connectMulti(t, primaryNode(t, "n1", "a1", &calls, &mu), primaryNode(t, "n2", "b1", &calls, &mu))
	if err := c.Call(api.MethodBotSetPrimary, api.BotPrimaryParams{}, nil); err == nil {
		t.Fatal("unset without node_id on several nodes succeeded")
	}
	if err := c.Call(api.MethodBotSetPrimary, api.BotPrimaryParams{NodeID: "n2"}, nil); err != nil {
		t.Fatalf("unset: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(calls, []string{"n2="}) {
		t.Fatalf("calls = %v", calls)
	}
}

// TestBotChangedWhenChannelReopens: a node whose channel reopens after
// it was lost gets a bot.changed, so its bots stop showing unreachable.
func TestBotChangedWhenChannelReopens(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		b, _ := json.Marshal(api.BotListResult{Bots: []api.BotInfo{{ID: "a1", Status: "idle"}}})
		return b, nil, nil
	}
	c, err := NewE2EClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	var res api.BotListResult
	if err := c.Call(api.MethodBotList, nil, &res); err != nil {
		t.Fatal(err)
	}
	c.loseNode("n1", false)
	drain(c)
	c.adoptNode(api.NodeDescriptor{ID: "n1", Label: "n1-box", Online: true, IdentityPubKey: base64.StdEncoding.EncodeToString(f.nodeKey.Public)})
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Method == api.MethodBotChanged && string(ev.Params) == `{"node_id":"n1"}` {
				return
			}
		case <-timeout:
			t.Fatal("no bot.changed on reopen")
		}
	}
}

func drain(c *E2EClient) {
	for {
		select {
		case <-c.Events():
		default:
			return
		}
	}
}
