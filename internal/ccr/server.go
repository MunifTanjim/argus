package ccr

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServerConfig configures the ingress server.
type ServerConfig struct {
	// Hostname is the allowlisted hostname we serve (default
	// api.anthropic.com). The worker's --sdk-url must be under
	// https://<Hostname>[:port]/v1/code/sessions.
	Hostname string
	// AdminToken guards the /client/* controller API. Minted when empty.
	AdminToken string
	// Spawner, when non-nil, lets POST /client/sessions spawn the worker.
	Spawner *SpawnerConfig
	// Upstream, when non-empty, reverse-proxies non-ingress paths to this
	// origin (e.g. https://api.anthropic.com) so a hosts-override machine
	// keeps working for normal API traffic. Empty disables proxying.
	Upstream string
}

// Server is the session-ingress server.
type Server struct {
	cfg ServerConfig

	mu       sync.Mutex
	sessions map[string]*Session
	workers  map[string]*Worker
	nextID   int
}

// NewServer builds a Server.
func NewServer(cfg ServerConfig) *Server {
	if cfg.Hostname == "" {
		cfg.Hostname = "api.anthropic.com"
	}
	if cfg.AdminToken == "" {
		cfg.AdminToken = randomHex(16)
	}
	return &Server{
		cfg:      cfg,
		sessions: map[string]*Session{},
		workers:  map[string]*Worker{},
	}
}

// AdminToken returns the controller token.
func (s *Server) AdminToken() string { return s.cfg.AdminToken }

// SetSpawner installs a spawner so POST /client/sessions (and the ingress
// POST /v1/code/sessions) launches the worker too.
func (s *Server) SetSpawner(cfg *SpawnerConfig) { s.cfg.Spawner = cfg }

// Handler returns the HTTP handler: the ingress API under /v1, the
// controller API under /client, and (optionally) a reverse proxy for
// everything else.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/code/sessions", s.authAny(s.handleCreateSession))
	mux.HandleFunc("/v1/code/sessions/{id}/worker/register", s.authAny(s.handleWorkerRegister))
	mux.HandleFunc("/v1/code/sessions/{id}/worker/events/stream", s.authAny(s.handleWorkerStream))
	mux.HandleFunc("/v1/code/sessions/{id}/worker/events", s.authAny(s.handleWorkerEvents))
	mux.HandleFunc("/v1/code/sessions/{id}/worker/events/delivery", s.authAny(s.handleNoBody))
	mux.HandleFunc("/v1/code/sessions/{id}/worker/heartbeat", s.authAny(s.handleNoBody))
	mux.HandleFunc("/v1/code/sessions/{id}/worker/internal-events", s.authAny(s.handleInternalEvents))
	mux.HandleFunc("/v1/code/sessions/{id}/worker", s.authAny(s.handleWorker))
	mux.HandleFunc("/v1/code/sessions/{id}", s.authAny(s.handleSessionRecord))
	mux.HandleFunc("/v1/code/sessions/{id}/bridge", s.authAny(s.handleBridge))

	mux.HandleFunc("POST /client/sessions", s.authAdmin(s.handleCreateSession))
	mux.HandleFunc("GET /client/sessions", s.authAdmin(s.handleListSessions))
	mux.HandleFunc("GET /client/sessions/{id}/events", s.authAdmin(s.handleClientEvents))
	mux.HandleFunc("POST /client/sessions/{id}/messages", s.authAdmin(s.handleClientMessage))
	mux.HandleFunc("POST /client/sessions/{id}/control", s.authAdmin(s.handleClientControl))
	mux.HandleFunc("POST /client/sessions/{id}/kill", s.authAdmin(s.handleClientKill))
	mux.HandleFunc("GET /client/sessions/{id}/log", s.authAdmin(s.handleClientLog))
	mux.HandleFunc("GET /client/info", s.authAdmin(s.handleInfo))

	if s.cfg.Upstream != "" {
		up, err := url.Parse(s.cfg.Upstream)
		if err == nil {
			proxy := httputil.NewSingleHostReverseProxy(up)
			proxy.Transport = newUpstreamTransport(up)
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				r.Host = up.Host
				proxy.ServeHTTP(w, r)
			})
		}
	}

	return mux
}

// newUpstreamTransport builds a Transport for proxying to the real upstream.
// A hosts/DNS override of the ingress hostname must NOT apply, so all
// upstream connections resolve through public DNS directly and do not touch
// the system resolver (which on this machine points the hostname back at us).
func newUpstreamTransport(up *url.URL) *http.Transport {
	host := up.Hostname()
	port := up.Port()
	if port == "" {
		port = "443"
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", "1.1.1.1:53")
		},
	}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			ips, err := resolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("ccr: resolving upstream: %w", err)
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(ips[0].String(), port))
		},
		TLSClientConfig: &tls.Config{ServerName: host},
	}
}

// ---- auth ----

// authAny accepts either a session worker token (on the session's own paths)
// or the admin token. The worker paths are additionally checked against the
// session named in the URL.
func (s *Server) authAny(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			http.Error(w, `{"error":{"type":"authentication_error","message":"missing bearer token"}}`, 401)
			return
		}
		id := r.PathValue("id")
		if id != "" {
			sess := s.session(id)
			if sess == nil {
				http.Error(w, `{"error":{"type":"not_found_error","resource":"session"}}`, 404)
				return
			}
			if token != sess.Token && token != s.cfg.AdminToken {
				http.Error(w, `{"error":{"type":"authentication_error","message":"invalid worker token"}}`, 401)
				return
			}
			h(w, r)
			return
		}
		if token != s.cfg.AdminToken {
			http.Error(w, `{"error":{"type":"authentication_error","message":"invalid token"}}`, 401)
			return
		}
		h(w, r)
	}
}

func (s *Server) authAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bearer(r) != s.cfg.AdminToken {
			http.Error(w, `{"error":{"type":"authentication_error","message":"invalid admin token"}}`, 401)
			return
		}
		h(w, r)
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

// ---- shared helpers ----

func (s *Server) session(id string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readBody(r *http.Request, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 32 << 20
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ---- ingress: session lifecycle ----

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title  string `json:"title"`
		Config struct {
			Cwd   string `json:"cwd"`
			Model string `json:"model"`
		} `json:"config"`
	}
	b, err := readBody(r, 1<<20)
	if err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}

	sess, info := s.createSession(body.Title)
	if s.cfg.Spawner != nil && s.cfg.Spawner.Claude != "" {
		cwd := body.Config.Cwd
		if cwd == "" {
			cwd = "."
		}
		spawnCfg := *s.cfg.Spawner
		if body.Config.Model != "" {
			spawnCfg.Env = append(spawnCfg.Env, "ANTHROPIC_MODEL="+body.Config.Model)
		}
		if wk, err := SpawnWorker(context.Background(), spawnCfg, sess, cwd); err != nil {
			log.Printf("ccr: spawn worker for %s: %v", sess.ID, err)
		} else {
			s.TrackWorker(sess, wk)
		}
	}
	writeJSON(w, 201, map[string]any{
		"session": map[string]string{"id": sess.ID},
		"_info":   info,
	})
}

func (s *Server) createSession(title string) (*Session, map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	sess := newSession(fmt.Sprintf("cse_%s%04d", randomHex(8), s.nextID))
	s.sessions[sess.ID] = sess
	return sess, map[string]any{"id": sess.ID, "title": title, "worker_token": sess.Token}
}

func (s *Server) handleSessionRecord(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r.PathValue("id"))
	if sess == nil {
		http.Error(w, `{"error":{"type":"not_found_error","resource":"session"}}`, 404)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"session": sess.Info()})
	case http.MethodPut, http.MethodPatch:
		b, _ := readBody(r, 1<<20)
		writeJSON(w, 200, map[string]any{"session": json.RawMessage(orEmpty(b))})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleBridge implements the official bridge attach: a bridge host calls
// POST /bridge to claim the session and receive a worker JWT, the epoch, and
// the api base to dial. We mint the session's worker token as the JWT.
func (s *Server) handleBridge(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r.PathValue("id"))
	if sess == nil {
		http.Error(w, `{"error":{"type":"not_found_error","resource":"session"}}`, 404)
		return
	}
	epoch := sess.RegisterWorker()
	apiBase := "https://" + r.Host
	writeJSON(w, 200, map[string]any{
		"worker_jwt":   sess.Token,
		"expires_in":   3600,
		"api_base_url": apiBase,
		"worker_epoch": epoch,
	})
}

func orEmpty(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(b)
}

// ---- ingress: worker lane ----

func (s *Server) handleWorkerRegister(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r.PathValue("id"))
	epoch := sess.RegisterWorker()
	writeJSON(w, 200, map[string]any{"worker_epoch": epoch})
}

func (s *Server) handleWorker(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r.PathValue("id"))
	switch r.Method {
	case http.MethodGet:
		// GET /worker?include_hearth=true — worker state read on resume.
		writeJSON(w, 200, map[string]any{
			"worker": map[string]any{
				"external_metadata": nil,
				"internal_metadata": nil,
			},
		})
	case http.MethodPut:
		b, _ := readBody(r, 4<<20)
		sess.SaveWorkerState(orEmpty(b))
		// No upload_stream offer: the worker uploads over plain HTTP.
		writeJSON(w, 200, map[string]any{})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleNoBody(w http.ResponseWriter, r *http.Request) {
	// heartbeat + delivery acks: consume the body, answer 200. An optional
	// heartbeat_interval_seconds in the response retunes the cadence.
	b, _ := readBody(r, 1<<20)
	resp := map[string]any{}
	var probe struct {
		CurrentIntervalSeconds float64 `json:"current_interval_seconds"`
	}
	if json.Unmarshal(b, &probe) == nil && probe.CurrentIntervalSeconds > 0 {
		resp["heartbeat_interval_seconds"] = int64(probe.CurrentIntervalSeconds)
	}
	writeJSON(w, 200, resp)
}

// handleWorkerEvents serves the shared POST path: batched uploads
// ({worker_epoch, events:[…]}) and the raw single-message lane.
func (s *Server) handleWorkerEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	sess := s.session(r.PathValue("id"))
	b, err := readBody(r, 64<<20)
	if err != nil {
		http.Error(w, "read error", 400)
		return
	}

	var batch struct {
		WorkerEpoch int64 `json:"worker_epoch"`
		Events      []struct {
			Payload    json.RawMessage `json:"payload"`
			Historical bool            `json:"historical"`
			Ephemeral  bool            `json:"ephemeral"`
		} `json:"events"`
	}
	if err := json.Unmarshal(b, &batch); err == nil && batch.Events != nil {
		var payloads []json.RawMessage
		for _, e := range batch.Events {
			payloads = append(payloads, e.Payload)
		}
		sess.RecordUpload(payloads)
		writeJSON(w, 200, map[string]any{"accepted": len(payloads)})
		return
	}

	// Raw SDK message lane: treat the whole body as one payload. It is both
	// recorded (for controllers) and, for user messages, not pushed back to
	// the worker (the SDK already sees its own input).
	var probe struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(b, &probe)
	sess.RecordUpload([]json.RawMessage{json.RawMessage(b)})
	writeJSON(w, 200, map[string]any{})
}

func (s *Server) handleInternalEvents(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r.PathValue("id"))
	switch r.Method {
	case http.MethodPost:
		b, err := readBody(r, 64<<20)
		if err != nil {
			http.Error(w, "read error", 400)
			return
		}
		var batch struct {
			WorkerEpoch int64 `json:"worker_epoch"`
			Events      []struct {
				Payload json.RawMessage `json:"payload"`
			} `json:"events"`
		}
		if err := json.Unmarshal(b, &batch); err != nil || batch.Events == nil {
			http.Error(w, `{"error":{"type":"invalid_request_error"}}`, 400)
			return
		}
		var rows []json.RawMessage
		for _, e := range batch.Events {
			rows = append(rows, e.Payload)
		}
		sess.RecordInternal(rows)
		writeJSON(w, 200, map[string]any{})
	case http.MethodGet:
		cursor := int64(0)
		if v := r.URL.Query().Get("cursor"); v != "" {
			cursor, _ = strconv.ParseInt(v, 10, 64)
		}
		subagents := r.URL.Query().Get("subagents") == "true"
		agentID := r.URL.Query().Get("session_agent_id")
		rows, next := sess.InternalPage(cursor, subagents, agentID)
		resp := map[string]any{"data": rows}
		if next >= 0 {
			resp["next_cursor"] = strconv.FormatInt(next, 10)
		}
		writeJSON(w, 200, resp)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleWorkerStream is the inbound SSE stream to the worker.
func (s *Server) handleWorkerStream(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r.PathValue("id"))
	if sess == nil {
		http.Error(w, `{"error":{"type":"not_found_error","resource":"session"}}`, 404)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}

	fromSeq := int64(0)
	if v := r.URL.Query().Get("from_sequence_num"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			fromSeq = n
		}
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > fromSeq {
			fromSeq = n
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("x-ccr-sse-keepalive-interval-seconds", "15")
	w.WriteHeader(200)
	fl.Flush()

	// Replay retained frames after the resume marker.
	for _, ev := range sess.InboundSince(fromSeq) {
		_, _ = w.Write(sseFrame("client_event", ev.Seq, ev.Frame))
		fl.Flush()
	}
	_, _ = w.Write([]byte(": connected\n\n"))
	fl.Flush()

	ch, done := sess.SubInbound()
	defer done()

	keepalive := time.NewTicker(12 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			_, _ = w.Write(sseFrame("client_event", ev.Seq, ev.Frame))
			fl.Flush()
			log.Printf("ccr: SSE -> worker %s seq=%d bytes=%d", sess.ID, ev.Seq, len(ev.Frame))
		case <-keepalive.C:
			// SSE comment keepalive. The worker treats keepalive interval + 30s
			// of silence as a dead stream, so 12s ticks are comfortably inside.
			_, _ = w.Write([]byte(": keepalive\n\n"))
			fl.Flush()
		}
	}
}

// ---- controller API ----

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	workers := map[string]*Worker{}
	for id, wk := range s.workers {
		workers[id] = wk
	}
	s.mu.Unlock()

	out := make([]map[string]any, 0, len(sessions))
	for _, sess := range sessions {
		m := map[string]any{"session": sess.Info(), "worker_attached": sess.HasWorker()}
		if wk := workers[sess.ID]; wk != nil {
			m["worker_exited"] = wk.Exited()
			if wk.Exited() {
				m["worker_error"] = wk.Err().Error()
			}
		}
		out = append(out, m)
	}
	writeJSON(w, 200, map[string]any{"sessions": out, "count": len(ids)})
}

func (s *Server) handleClientMessage(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r.PathValue("id"))
	if sess == nil {
		http.Error(w, `{"error":{"type":"not_found_error"}}`, 404)
		return
	}
	if !sess.HasWorker() {
		http.Error(w, `{"error":{"type":"api_error","message":"worker not connected"}}`, 409)
		return
	}
	var body struct {
		Text       string `json:"text"`
		PasteImage string `json:"paste_image"` // base64, optional
		UUID       string `json:"uuid"`
	}
	b, err := readBody(r, 8<<20)
	if err != nil || json.Unmarshal(b, &body) != nil || strings.TrimSpace(body.Text) == "" {
		http.Error(w, `{"error":{"type":"invalid_request_error","message":"body must be {\"text\": \"…\"}"}}`, 400)
		return
	}

	content := []any{map[string]any{"type": "text", "text": body.Text}}
	if body.PasteImage != "" {
		content = append(content, map[string]any{"type": "image", "source": map[string]any{
			"type": "base64", "media_type": "image/png", "data": body.PasteImage,
		}})
	}
	msg := map[string]any{
		"type":               "user",
		"session_id":         "",
		"message":            map[string]any{"role": "user", "content": content},
		"parent_tool_use_id": nil,
		"inbound_origin":     map[string]string{"kind": "human"},
		"client_platform":    "cli",
	}
	if body.UUID != "" {
		msg["uuid"] = body.UUID
	}
	payload, _ := json.Marshal(msg)
	sess.PushInbound(payload, "user")
	writeJSON(w, 200, map[string]any{"sent": true})
}

// handleClientControl relays a control_response (or control_cancel_request)
// to the worker. The body is passed through as the client_event payload.
func (s *Server) handleClientControl(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r.PathValue("id"))
	if sess == nil {
		http.Error(w, `{"error":{"type":"not_found_error"}}`, 404)
		return
	}
	b, err := readBody(r, 1<<20)
	if err != nil || len(b) == 0 {
		http.Error(w, `{"error":{"type":"invalid_request_error"}}`, 400)
		return
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &probe); err != nil ||
		(probe.Type != "control_response" && probe.Type != "control_cancel_request" && probe.Type != "control_request") {
		http.Error(w, `{"error":{"type":"invalid_request_error","message":"body must be a control_response / control_cancel_request / control_request"}}`, 400)
		return
	}
	sess.PushInbound(json.RawMessage(b), probe.Type)
	writeJSON(w, 200, map[string]any{"sent": true})
}

// handleClientEvents streams every uploaded worker event to a controller.
func (s *Server) handleClientEvents(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r.PathValue("id"))
	if sess == nil {
		http.Error(w, `{"error":{"type":"not_found_error"}}`, 404)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	after := int64(0)
	if v := r.URL.Query().Get("after"); v != "" {
		after, _ = strconv.ParseInt(v, 10, 64)
	}
	events, _ := sess.UploadsSince(after)

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	for _, e := range events {
		_, _ = w.Write(sseFrame("worker_event", 0, e))
	}
	_, _ = w.Write([]byte(": connected\n\n"))
	fl.Flush()

	ch, done := sess.SubUploads()
	defer done()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-ch:
			if !ok {
				_, _ = w.Write(sseFrame("worker_event", 0, []byte(`{"type":"system","subtype":"session_closed"}`)))
				fl.Flush()
				return
			}
			_, _ = w.Write(sseFrame("worker_event", 0, frame))
			fl.Flush()
		case <-keepalive.C:
			_, _ = w.Write([]byte(": keepalive\n\n"))
			fl.Flush()
		}
	}
}

func (s *Server) handleClientKill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	wk := s.workers[id]
	delete(s.workers, id)
	s.mu.Unlock()
	sess := s.session(id)
	if sess == nil {
		http.Error(w, `{"error":{"type":"not_found_error"}}`, 404)
		return
	}
	if wk != nil && wk.Cmd != nil && wk.Cmd.Process != nil {
		_ = wk.Cmd.Process.Kill()
	}
	sess.MarkDead()
	writeJSON(w, 200, map[string]any{"killed": true})
}

func (s *Server) handleClientLog(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	wk := s.workers[r.PathValue("id")]
	s.mu.Unlock()
	if wk == nil {
		http.Error(w, `{"error":{"type":"not_found_error","message":"no spawned worker"}}`, 404)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(wk.TailLog(64 << 10))
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"hostname":    s.cfg.Hostname,
		"upstream":    s.cfg.Upstream,
		"has_spawner": s.cfg.Spawner != nil && s.cfg.Spawner.Claude != "",
	})
}

// TrackWorker records a spawned worker so the controller API can see it.
func (s *Server) TrackWorker(sess *Session, wk *Worker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[sess.ID] = wk
}
