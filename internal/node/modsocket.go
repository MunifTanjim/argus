package node

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// ModSocketPath is the HTTP socket agent mods (Claude Code) reach argusd on,
// named after the JSON-RPC socket at socketPath: argus.sock -> argus.mod.sock.
func ModSocketPath(socketPath string) string {
	return strings.TrimSuffix(socketPath, ".sock") + ".mod.sock"
}

// maxSocketPath is the longest path a unix socket can bind to on this OS.
var maxSocketPath = len(syscall.RawSockaddrUnix{}.Path) - 1

func checkSocketPath(path string) error {
	if len(path) > maxSocketPath {
		return fmt.Errorf("socket path %s is %d bytes, over the %d-byte unix socket limit: use a shorter --socket", path, len(path), maxSocketPath)
	}
	return nil
}

// modHold caps how long a mod's poll or decision wait is held open before it
// is answered empty, under the 30 s a mod's $.http.fetch may take. A var so
// tests can shorten it.
var modHold = 25 * time.Second

// modEvent is a classic hook event as the mod forwards it.
type modEvent struct {
	Event    string          `json:"event"`
	Payload  json.RawMessage `json:"payload"`
	TmuxPane string          `json:"tmuxPane"`
	Tmux     string          `json:"tmux"` // the session's $TMUX
	AutoMode bool            `json:"autoMode"`
}

type modEventResult struct {
	RequestID string `json:"requestId,omitempty"`
}

type modRequest struct {
	RequestID string `json:"requestId"`
}

type modDecisionResult struct {
	Done   bool   `json:"done"`
	Output string `json:"output,omitempty"`
}

type modPoll struct {
	SessionID string   `json:"sessionId"`
	Kinds     []string `json:"kinds"`
}

type modAck struct {
	ID    string          `json:"id"`
	Error string          `json:"error,omitempty"`
	Ran   bool            `json:"ran,omitempty"` // ran, not only accepted to run later
	Data  json.RawMessage `json:"data,omitempty"`
}

type modPollResult struct {
	Commands []modbridge.Command `json:"commands"`
}

func (d *Node) modHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{agent}/event", d.modRoute(d.handleModEvent))
	mux.HandleFunc("POST /{agent}/decision", d.modRoute(d.handleModDecision))
	mux.HandleFunc("POST /{agent}/cancel", d.modRoute(d.handleModCancel))
	mux.HandleFunc("POST /{agent}/poll", d.modRoute(d.handleModPoll))
	mux.HandleFunc("POST /{agent}/ack", d.modRoute(d.handleModAck))
	return mux
}

// modRoute resolves the agent a mod request is for and caps its body.
func (d *Node) modRoute(h func(http.ResponseWriter, *http.Request, adapter.Adapter)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := d.adapters[r.PathValue("agent")]
		if !ok {
			http.Error(w, "unknown agent", http.StatusNotFound)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, adapter.MaxHookPayload)
		h(w, r, a)
	}
}

// decodeMod decodes a mod request body, answering 400 with msg when it does not
// decode or valid rejects it.
func decodeMod[T any](w http.ResponseWriter, r *http.Request, valid func(T) bool, msg string) (T, bool) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil || !valid(v) {
		http.Error(w, msg, http.StatusBadRequest)
		return v, false
	}
	return v, true
}

// modBridge is a's bridge, or nil (and a 404 sent) when a has none.
func modBridge(w http.ResponseWriter, a adapter.Adapter) *modbridge.Bridge {
	if mb, ok := a.(adapter.ModBridged); ok && mb.ModBridge() != nil {
		return mb.ModBridge()
	}
	http.Error(w, "agent has no mod bridge", http.StatusNotFound)
	return nil
}

// handleModEvent applies a forwarded event as a hook would. A PermissionRequest
// is parked and answered with the id the mod waits on, never held open.
func (d *Node) handleModEvent(w http.ResponseWriter, r *http.Request, a adapter.Adapter) {
	in, ok := decodeMod(w, r, func(modEvent) bool { return true }, "invalid event")
	if !ok {
		return
	}
	ev := adapter.HookEvent{
		Agent:      a.Agent(),
		Event:      in.Event,
		TmuxPane:   in.TmuxPane,
		TmuxSocket: tmux.SocketBaseFromEnv(in.Tmux),
		Payload:    in.Payload,
		AutoMode:   in.AutoMode,
	}
	s, alive := d.applyHook(r.Context(), a, ev)
	if in.Event == "PostToolUse" || in.Event == "PostToolUseFailure" {
		d.endModPermissionsRan(in.Payload)
	}
	var res modEventResult
	if alive && a.ShouldBlock(ev) {
		res.RequestID = d.startModPermission(a, s, ev)
	}
	writeModJSON(w, res)
}

func (d *Node) handleModDecision(w http.ResponseWriter, r *http.Request, _ adapter.Adapter) {
	in, ok := decodeMod(w, r, func(v modRequest) bool { return v.RequestID != "" }, "requestId is required")
	if !ok {
		return
	}
	out, done := d.waitModPermission(r.Context(), in.RequestID)
	if r.Context().Err() != nil {
		return // the mod hung up
	}
	writeModJSON(w, modDecisionResult{Done: done, Output: out})
}

// handleModCancel ends a permission request the agent decided itself.
func (d *Node) handleModCancel(w http.ResponseWriter, r *http.Request, _ adapter.Adapter) {
	in, ok := decodeMod(w, r, func(v modRequest) bool { return v.RequestID != "" }, "requestId is required")
	if !ok {
		return
	}
	d.cancelModPermission(in.RequestID)
	writeModJSON(w, struct{}{})
}

func (d *Node) handleModPoll(w http.ResponseWriter, r *http.Request, a adapter.Adapter) {
	in, ok := decodeMod(w, r, func(v modPoll) bool { return v.SessionID != "" }, "sessionId is required")
	if !ok {
		return
	}
	b := modBridge(w, a)
	if b == nil {
		return
	}
	cmds := b.Wait(r.Context(), in.SessionID, modHold, in.Kinds)
	if cmds == nil {
		cmds = []modbridge.Command{}
	}
	writeModJSON(w, modPollResult{Commands: cmds})
}

func (d *Node) handleModAck(w http.ResponseWriter, r *http.Request, a adapter.Adapter) {
	in, ok := decodeMod(w, r, func(v modAck) bool { return v.ID != "" }, "id is required")
	if !ok {
		return
	}
	b := modBridge(w, a)
	if b == nil {
		return
	}
	if in.Error != "" {
		d.log.Warn("mod command failed", "agent", a.Agent(), "id", in.ID, "err", in.Error)
	}
	b.Ack(in.ID, in.Error, in.Ran, in.Data)
	writeModJSON(w, struct{}{})
}

func writeModJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
