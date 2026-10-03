package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestRPCInitializeHandshake(t *testing.T) {
	f := newFakeDaemon(t, nil)
	c, err := dialRPC(context.Background(), f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if err := c.initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := f.expect(t, "initialize")
	var p struct {
		ClientInfo struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
		Capabilities struct {
			ExperimentalAPI bool `json:"experimentalApi"`
		} `json:"capabilities"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if p.ClientInfo.Name != "argus" || !p.Capabilities.ExperimentalAPI {
		t.Fatalf("initialize params = %s", m.Params)
	}
	if n := f.expect(t, "initialized"); n.ID != nil {
		t.Fatalf("initialized must be a notification, got id %s", n.ID)
	}
}

func TestRPCCallResultAndError(t *testing.T) {
	f := newFakeDaemon(t, func(method string, _ json.RawMessage) (any, *rpcError) {
		if method == "bad" {
			return nil, &rpcError{Code: -32600, Message: "no rollout found"}
		}
		return map[string]any{"data": []string{"t1"}}, nil
	})
	c, err := dialRPC(context.Background(), f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

	var res struct{ Data []string }
	if err := c.call(context.Background(), "thread/loaded/list", map[string]any{}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Data) != 1 || res.Data[0] != "t1" {
		t.Fatalf("result = %+v", res)
	}

	err = c.call(context.Background(), "bad", nil, nil)
	var re *rpcError
	if !errors.As(err, &re) || re.Code != -32600 {
		t.Fatalf("err = %v; want rpcError -32600", err)
	}
}

func TestRPCInboundNotificationAndRequestThenReply(t *testing.T) {
	f := newFakeDaemon(t, nil)
	c, err := dialRPC(context.Background(), f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	f.waitConns(t, 1)

	f.push("thread/closed", map[string]any{"threadId": "t1"})
	f.request(0, "item/commandExecution/requestApproval", map[string]any{"threadId": "t1"})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n, ok := c.next(ctx)
	if !ok || n.Method != "thread/closed" || n.ID != nil {
		t.Fatalf("first inbound = %+v ok=%v", n, ok)
	}
	r, ok := c.next(ctx)
	if !ok || r.Method != "item/commandExecution/requestApproval" || string(r.ID) != "0" {
		t.Fatalf("second inbound = %+v ok=%v", r, ok)
	}
	if err := c.reply(ctx, r.ID, map[string]string{"decision": "accept"}); err != nil {
		t.Fatal(err)
	}
	got := f.expect(t, "")
	if string(got.ID) != "0" || string(got.Result) != `{"decision":"accept"}` {
		t.Fatalf("reply = id %s result %s", got.ID, got.Result)
	}
}

// A call made while inbound messages pile up must still get its response: the
// read loop never blocks on the consumer.
func TestRPCCallNotBlockedByUnreadInbound(t *testing.T) {
	f := newFakeDaemon(t, nil)
	c, err := dialRPC(context.Background(), f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	f.waitConns(t, 1)
	for i := 0; i < 2000; i++ {
		f.push("item/agentMessage/delta", map[string]any{"delta": "x"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.call(ctx, "thread/loaded/list", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRPCDoneOnServerClose(t *testing.T) {
	f := newFakeDaemon(t, nil)
	c, err := dialRPC(context.Background(), f.sock)
	if err != nil {
		t.Fatal(err)
	}
	f.waitConns(t, 1)
	f.closeConns()
	select {
	case <-c.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("done not closed after server close")
	}
	if _, ok := c.next(context.Background()); ok {
		t.Fatal("next must report !ok after close")
	}
}
