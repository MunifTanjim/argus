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

// primaryNode is a node whose primary was set at since; its SetPrimary calls
// are recorded, and fail when failSet is set.
func primaryNode(t *testing.T, id, agent string, since time.Time, failSet bool, calls *[]string, mu *sync.Mutex) *fakeNode {
	t.Helper()
	k, _ := e2e.GenerateKeyPair()
	return &fakeNode{id: id, key: k, handle: func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodPAgentList:
			b, _ := json.Marshal(api.PAgentListResult{Agents: []api.PAgentInfo{{ID: agent, Primary: true}}, Primary: agent, PrimarySince: since})
			return b, nil, nil
		case api.MethodPAgentSetPrimary:
			var p api.PAgentPrimaryParams
			_ = json.Unmarshal(params, &p)
			mu.Lock()
			*calls = append(*calls, id+"="+p.AgentID)
			mu.Unlock()
			if failSet {
				return nil, &api.RPCError{Code: api.CodeInternalError, Message: "disk full"}, nil
			}
		}
		return json.RawMessage(`null`), nil, nil
	}}
}

func waitCalls(t *testing.T, mu *sync.Mutex, calls *[]string, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := slices.Contains(*calls, want)
		mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("calls = %v, want %s", *calls, want)
}

// TestPAgentListNewestPrimaryWins: two nodes report a primary (one came back
// after the user picked another); the newer one is kept and the older one is
// cleared on its node.
func TestPAgentListNewestPrimaryWins(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	now := time.Now().UTC()
	a := primaryNode(t, "n1", "a1", now.Add(-time.Hour), false, &calls, &mu)
	b := primaryNode(t, "n2", "b1", now, false, &calls, &mu)
	c := connectMulti(t, a, b)
	var res api.PAgentListResult
	if err := c.Call(api.MethodPAgentList, nil, &res); err != nil {
		t.Fatal(err)
	}
	if res.Primary != "n2:b1" || len(res.Agents) != 2 || res.Agents[0].Primary || !res.Agents[1].Primary {
		t.Fatalf("res %+v", res)
	}
	waitCalls(t, &mu, &calls, "n1=")
	mu.Lock()
	defer mu.Unlock()
	if slices.Contains(calls, "n2=") {
		t.Errorf("newest primary cleared: %v", calls)
	}
}

// TestPAgentSetPrimaryIgnoresUnsetFailure: the set succeeded, so a node that
// fails to clear its old primary doesn't fail the call.
func TestPAgentSetPrimaryIgnoresUnsetFailure(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	a := primaryNode(t, "n1", "a1", time.Time{}, false, &calls, &mu)
	b := primaryNode(t, "n2", "b1", time.Time{}, true, &calls, &mu)
	c := connectMulti(t, a, b)
	if err := c.Call(api.MethodPAgentSetPrimary, api.PAgentPrimaryParams{AgentID: "n1:a1"}, nil); err != nil {
		t.Fatalf("set: %v", err)
	}
	waitCalls(t, &mu, &calls, "n2=")
}

// TestPAgentChangedWhenChannelReopens: a node whose channel reopens after
// it was lost gets a pagent.changed, so its agents stop showing unreachable.
func TestPAgentChangedWhenChannelReopens(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		b, _ := json.Marshal(api.PAgentListResult{Agents: []api.PAgentInfo{{ID: "a1", Status: "idle"}}})
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
	var res api.PAgentListResult
	if err := c.Call(api.MethodPAgentList, nil, &res); err != nil {
		t.Fatal(err)
	}
	c.loseNode("n1", false)
	drain(c)
	c.adoptNode(api.NodeDescriptor{ID: "n1", Label: "n1-box", Online: true, IdentityPubKey: base64.StdEncoding.EncodeToString(f.nodeKey.Public)})
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Method == api.MethodPAgentChanged && string(ev.Params) == `{"node_id":"n1"}` {
				return
			}
		case <-timeout:
			t.Fatal("no pagent.changed on reopen")
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
