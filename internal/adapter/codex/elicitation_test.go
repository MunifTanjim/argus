package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func optionValues(ix *session.Interaction) []string {
	var vs []string
	for _, o := range ix.Options {
		vs = append(vs, o.Value)
	}
	return vs
}

func TestParseToolApprovalElicitation(t *testing.T) {
	params := `{"threadId":"t1","serverName":"github","mode":"form","message":"Allow the github MCP server to run tool \"create_issue\"?",
		"requestedSchema":{"type":"object","properties":{}},
		"_meta":{"codex_approval_kind":"mcp_tool_call","persist":["session","always"],"tool_params":{"title":"bug"}}}`
	pr, ok := parseRequest(inbound{ID: json.RawMessage(`1`), Method: "mcpServer/elicitation/request", Params: json.RawMessage(params)})
	if !ok || pr.threadID != "t1" {
		t.Fatalf("parse = %+v, %v", pr, ok)
	}
	ix := pr.interaction
	if ix.Kind != session.InteractionPermission || ix.ToolInput != `{"title":"bug"}` || ix.Message == "" {
		t.Fatalf("interaction = %+v", ix)
	}
	if got := optionValues(ix); !reflect.DeepEqual(got, []string{"allow", "acceptForSession", "acceptAlways", "cancel"}) {
		t.Fatalf("options = %v", got)
	}
	for value, want := range map[string]string{
		"allow":            `{"_meta":null,"action":"accept","content":null}`,
		"acceptForSession": `{"_meta":{"persist":"session"},"action":"accept","content":null}`,
		"acceptAlways":     `{"_meta":{"persist":"always"},"action":"accept","content":null}`,
		"cancel":           `{"_meta":null,"action":"cancel","content":null}`,
	} {
		b, _ := json.Marshal(replyFor(pr, api.RespondParams{OptionValue: value}))
		if string(b) != want {
			t.Errorf("%s = %s; want %s", value, b, want)
		}
	}
}

func TestParseGenericElicitationOffersDenyAndCancel(t *testing.T) {
	params := `{"threadId":"t1","serverName":"s","mode":"form","message":"Proceed?","requestedSchema":null}`
	pr, _ := parseRequest(inbound{ID: json.RawMessage(`1`), Method: "mcpServer/elicitation/request", Params: json.RawMessage(params)})
	if got := optionValues(pr.interaction); !reflect.DeepEqual(got, []string{"allow", "deny", "cancel"}) {
		t.Fatalf("options = %v", got)
	}
	b, _ := json.Marshal(replyFor(pr, api.RespondParams{OptionValue: "deny"}))
	if string(b) != `{"_meta":null,"action":"decline","content":null}` {
		t.Errorf("deny = %s", b)
	}
}

func TestElicitationArgusCannotShowOffersOnlyCancel(t *testing.T) {
	for name, params := range map[string]string{
		"form with fields": `{"threadId":"t1","mode":"form","message":"Name?","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}}}}`,
		"url":              `{"threadId":"t1","mode":"url","message":"Sign in","url":"https://x"}`,
		"other approval":   `{"threadId":"t1","mode":"form","message":"Install?","requestedSchema":null,"_meta":{"codex_approval_kind":"tool_suggestion"}}`,
	} {
		pr, ok := parseRequest(inbound{ID: json.RawMessage(`1`), Method: "mcpServer/elicitation/request", Params: json.RawMessage(params)})
		if !ok || !reflect.DeepEqual(optionValues(pr.interaction), []string{"cancel"}) {
			t.Errorf("%s: interaction = %+v", name, pr.interaction)
			continue
		}
		// Even an allow cannot accept a request argus did not show.
		b, _ := json.Marshal(replyFor(pr, api.RespondParams{Behavior: "allow"}))
		if string(b) != `{"_meta":null,"action":"cancel","content":null}` {
			t.Errorf("%s: allow = %s", name, b)
		}
	}
}

func TestDynamicToolCallCanBeCancelled(t *testing.T) {
	params := `{"threadId":"t1","turnId":"u1","callId":"c1","namespace":"ide","tool":"open_file","arguments":{}}`
	pr, ok := parseRequest(inbound{ID: json.RawMessage(`1`), Method: "item/tool/call", Params: json.RawMessage(params)})
	if !ok || !reflect.DeepEqual(optionValues(pr.interaction), []string{"cancel"}) {
		t.Fatalf("interaction = %+v", pr.interaction)
	}
	b, _ := json.Marshal(replyFor(pr, api.RespondParams{OptionValue: "cancel"}))
	if string(b) != `{"contentItems":[{"text":"The user cancelled this tool call.","type":"inputText"}],"success":false}` {
		t.Errorf("reply = %s", b)
	}
}

// A waiting thread whose request argus cannot render still shows it, rather
// than a Working card that never changes.
func TestUnrenderableElicitationShowsAndCancels(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.request(9, "mcpServer/elicitation/request", map[string]any{
		"threadId": "t1", "serverName": "s", "mode": "url", "message": "Sign in", "url": "https://x", "elicitationId": "e1",
	})
	s := waitSession(t, reg, "t1", func(s session.Session) bool {
		return s.Status == session.StatusAwaitingInput && s.Interaction != nil && s.Interaction.Kind == session.InteractionPermission
	})
	if err := d.respond(context.Background(), s, api.RespondParams{Kind: "permission", RequestID: s.Interaction.RequestID, OptionValue: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if got := f.expect(t, ""); string(got.ID) != "9" || string(got.Result) != `{"_meta":null,"action":"cancel","content":null}` {
		t.Fatalf("reply = id %s result %s", got.ID, got.Result)
	}
}
