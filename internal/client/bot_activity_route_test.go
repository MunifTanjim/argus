package client

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestBotActivityCompositesIDs(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var gotBot string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodBotActivity {
			return nil, nil, nil
		}
		var p api.BotActivityParams
		_ = json.Unmarshal(params, &p)
		gotBot = p.BotID
		b, _ := json.Marshal(api.BotActivityResult{Activity: []api.BotActivity{{ThreadID: "t1", SessionID: "s1", Decision: "deny"}}})
		return b, nil, nil
	}
	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	var res api.BotActivityResult
	if err := c.Call(api.MethodBotActivity, api.BotActivityParams{BotID: "n1:a1"}, &res); err != nil {
		t.Fatal(err)
	}
	if gotBot != "a1" || len(res.Activity) != 1 || res.Activity[0].ThreadID != "n1:t1" || res.Activity[0].SessionID != "n1:s1" {
		t.Errorf("bot %q activity %+v", gotBot, res.Activity)
	}
}

func TestBotSecretRoutesByBot(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var got []string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodBotSecretSet, api.MethodBotSecretList, api.MethodBotSecretRm:
			var p struct {
				BotID string `json:"bot_id"`
			}
			_ = json.Unmarshal(params, &p)
			got = append(got, method+" "+p.BotID)
			if method == api.MethodBotSecretList {
				b, _ := json.Marshal(api.BotSecretListResult{Secrets: []api.BotSecretInfo{{Name: "GH_TOKEN"}}})
				return b, nil, nil
			}
			return json.RawMessage(`{}`), nil, nil
		}
		return nil, nil, nil
	}
	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(api.MethodBotSecretSet, api.BotSecretSetParams{BotID: "n1:a1", Name: "GH_TOKEN", Value: "v"}, nil); err != nil {
		t.Fatal(err)
	}
	var list api.BotSecretListResult
	if err := c.Call(api.MethodBotSecretList, api.BotRef{BotID: "n1:a1"}, &list); err != nil || len(list.Secrets) != 1 {
		t.Fatalf("list = %+v %v", list, err)
	}
	if err := c.Call(api.MethodBotSecretRm, api.BotSecretRef{BotID: "n1:a1", Name: "GH_TOKEN"}, nil); err != nil {
		t.Fatal(err)
	}
	want := api.MethodBotSecretSet + " a1," + api.MethodBotSecretList + " a1," + api.MethodBotSecretRm + " a1"
	if strings.Join(got, ",") != want {
		t.Errorf("node got %v", got)
	}
}
