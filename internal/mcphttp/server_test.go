package mcphttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "secret"

type fakeBackend struct {
	calls  int
	agent  string
	name   string
	meta   map[string]json.RawMessage
	args   json.RawMessage
	result CallResult
}

func (f *fakeBackend) Tools() []Tool {
	return []Tool{{Name: "echo", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}}
}

func (f *fakeBackend) Call(_ context.Context, agent, name string, meta map[string]json.RawMessage, args json.RawMessage) CallResult {
	f.calls++
	f.agent, f.name, f.meta, f.args = agent, name, meta, args
	return f.result
}

func post(t *testing.T, h http.Handler, path, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func rpc(t *testing.T, h http.Handler, body string) map[string]json.RawMessage {
	t.Helper()
	rec := post(t, h, "/mcp/codex", "Bearer "+testToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRejectsMissingOrWrongToken(t *testing.T) {
	b := &fakeBackend{}
	h := NewHandler(testToken, "1.2.3", b)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`
	for _, auth := range []string{"", "Bearer x", "secret", "Bearer "} {
		if rec := post(t, h, "/mcp/codex", auth, body); rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: status = %d", auth, rec.Code)
		}
	}
	if b.calls != 0 {
		t.Fatalf("backend called %d times", b.calls)
	}
}

func TestInitializeEchoesProtocolVersion(t *testing.T) {
	h := NewHandler(testToken, "1.2.3", &fakeBackend{})
	out := rpc(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	var res struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(out["result"], &res); err != nil {
		t.Fatal(err)
	}
	if res.ProtocolVersion != "2025-06-18" || res.ServerInfo.Name != "argus" || res.ServerInfo.Version != "1.2.3" || string(res.Capabilities) != `{"tools":{}}` {
		t.Fatalf("result = %s", out["result"])
	}
	if string(out["id"]) != "1" {
		t.Fatalf("id = %s", out["id"])
	}
}

func TestToolsListReturnsBackendTools(t *testing.T) {
	h := NewHandler(testToken, "1.2.3", &fakeBackend{})
	out := rpc(t, h, `{"jsonrpc":"2.0","id":"a","method":"tools/list"}`)
	var res struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(out["result"], &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "echo" || string(res.Tools[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("tools = %+v", res.Tools)
	}
}

func TestToolsCallPassesAgentMetaAndArgs(t *testing.T) {
	b := &fakeBackend{result: CallResult{Text: "hi"}}
	h := NewHandler(testToken, "1.2.3", b)
	out := rpc(t, h, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"x":1},"_meta":{"threadId":"t1"}}}`)
	if b.agent != "codex" || b.name != "echo" || string(b.meta["threadId"]) != `"t1"` || string(b.args) != `{"x":1}` {
		t.Fatalf("backend got %q %q %v %s", b.agent, b.name, b.meta, b.args)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(out["result"], &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 1 || res.Content[0].Type != "text" || res.Content[0].Text != "hi" || res.IsError {
		t.Fatalf("result = %s", out["result"])
	}
}

func TestToolsCallErrorResultSetsIsError(t *testing.T) {
	b := &fakeBackend{result: CallResult{Text: "boom", IsError: true}}
	out := rpc(t, NewHandler(testToken, "1.2.3", b), `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo"}}`)
	var res struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(out["result"], &res); err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("result = %s", out["result"])
	}
}

func TestInitializedNotificationReturns202(t *testing.T) {
	h := NewHandler(testToken, "1.2.3", &fakeBackend{})
	rec := post(t, h, "/mcp/codex", "Bearer "+testToken, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	h := NewHandler(testToken, "1.2.3", &fakeBackend{})
	out := rpc(t, h, `{"jsonrpc":"2.0","id":4,"method":"server/discover"}`)
	var e struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(out["error"], &e); err != nil {
		t.Fatal(err)
	}
	if e.Code != -32601 {
		t.Fatalf("error = %s", out["error"])
	}
}

func TestGetReturns405(t *testing.T) {
	h := NewHandler(testToken, "1.2.3", &fakeBackend{})
	req := httptest.NewRequest(http.MethodGet, "/mcp/codex", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}

func errorCode(t *testing.T, out map[string]json.RawMessage) int {
	t.Helper()
	var e struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(out["error"], &e); err != nil {
		t.Fatalf("error = %s: %v", out["error"], err)
	}
	return e.Code
}

func TestEmptyTokenRejectsEveryRequest(t *testing.T) {
	b := &fakeBackend{}
	h := NewHandler("", "1.2.3", b)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`
	for _, auth := range []string{"", "Bearer ", "Bearer x"} {
		if rec := post(t, h, "/mcp/codex", auth, body); rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: status = %d", auth, rec.Code)
		}
	}
	if b.calls != 0 {
		t.Fatalf("backend called %d times", b.calls)
	}
}

func TestMalformedJSONReturnsParseError(t *testing.T) {
	h := NewHandler(testToken, "1.2.3", &fakeBackend{})
	out := rpc(t, h, `{"jsonrpc":"2.0","id":1,`)
	if code := errorCode(t, out); code != -32700 {
		t.Fatalf("code = %d", code)
	}
	if string(out["id"]) != "null" {
		t.Fatalf("id = %s", out["id"])
	}
}

func TestOversizedBodyReturns413(t *testing.T) {
	b := &fakeBackend{}
	h := NewHandler(testToken, "1.2.3", b)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"x":"` + strings.Repeat("a", 1<<20) + `"}}}`
	if rec := post(t, h, "/mcp/codex", "Bearer "+testToken, body); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
	if b.calls != 0 {
		t.Fatalf("backend called %d times", b.calls)
	}
}

func TestBatchReturnsInvalidRequest(t *testing.T) {
	b := &fakeBackend{}
	h := NewHandler(testToken, "1.2.3", b)
	out := rpc(t, h, ` [{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}]`)
	if code := errorCode(t, out); code != -32600 {
		t.Fatalf("code = %d", code)
	}
	if b.calls != 0 {
		t.Fatalf("backend called %d times", b.calls)
	}
}

func TestClientResponseReturns202(t *testing.T) {
	h := NewHandler(testToken, "1.2.3", &fakeBackend{})
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":7,"result":{}}`,
		`{"jsonrpc":"2.0","id":7,"error":{"code":-32601,"message":"no"}}`,
	} {
		rec := post(t, h, "/mcp/codex", "Bearer "+testToken, body)
		if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
			t.Fatalf("%s: status = %d, body %q", body, rec.Code, rec.Body)
		}
	}
}

func TestInitializeDefaultsEmptyVersion(t *testing.T) {
	h := NewHandler(testToken, "", &fakeBackend{})
	out := rpc(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	var res struct {
		ServerInfo struct {
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(out["result"], &res); err != nil {
		t.Fatal(err)
	}
	if res.ServerInfo.Version != "0" {
		t.Fatalf("result = %s", out["result"])
	}
}
