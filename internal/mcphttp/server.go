package mcphttp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
)

const maxBodyBytes = 1 << 20

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type CallResult struct {
	Text    string
	IsError bool
}

type Backend interface {
	Tools() []Tool
	// Call runs a tool for the agent named in the URL path. meta is the request's params._meta.
	Call(ctx context.Context, agent, name string, meta map[string]json.RawMessage, args json.RawMessage) CallResult
}

// NewHandler serves JSON-RPC over POST /mcp/{agent}, guarded by a bearer token.
func NewHandler(token string, b Backend) http.Handler {
	s := &server{token: []byte(token), backend: b}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/{agent}", s.serve)
	return mux
}

type server struct {
	token   []byte
	backend Backend
}

func (s *server) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), s.token) == 1
}

func (s *server) serve(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &api.RPCError{Code: api.CodeParseError, Message: "parse error"}})
		return
	}
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, s.dispatch(r.Context(), r.PathValue("agent"), req))
}

func (s *server) dispatch(ctx context.Context, agent string, req request) response {
	resp := response{JSONRPC: "2.0", ID: req.ID}
	fail := func(code int, msg string) response {
		resp.Error = &api.RPCError{Code: code, Message: msg}
		return resp
	}
	switch req.Method {
	case "initialize":
		var p initializeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(api.CodeInvalidParams, "invalid params")
		}
		resp.Result = map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "argus"},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		tools := s.backend.Tools()
		if tools == nil {
			tools = []Tool{}
		}
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p callParams
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return fail(api.CodeInvalidParams, "invalid params")
		}
		res := s.backend.Call(ctx, agent, p.Name, p.Meta, p.Arguments)
		resp.Result = callResponse{Content: []textContent{{Type: "text", Text: res.Text}}, IsError: res.IsError}
	default:
		return fail(api.CodeMethodNotFound, "method not found")
	}
	return resp
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
