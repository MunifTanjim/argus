// Package ccr implements a self-hosted session-ingress server speaking the
// Claude Code Remote Control worker protocol (see
// devdocs/claude-remote-control-protocol.md).
//
// A headless worker (`claude --print --sdk-url …`) connects here as it would
// to Anthropic's backend: it registers, consumes an inbound SSE stream of SDK
// messages, and uploads its output through batched event POSTs. This server
// replaces the Anthropic side, so a worker can be driven without a claude.ai
// subscription.
package ccr

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Session is one remote-control session: one worker lane and any number of
// controller (client) subscribers.
type Session struct {
	ID    string
	Token string // bearer token the worker presents (worker_jwt equivalent)

	mu sync.Mutex

	epoch     int64
	dead      bool
	lastState json.RawMessage // most recent PUT /worker body

	inboundSeq  int64
	inboundLog  []InboundEvent         // replayable inbound frames (resume support)
	inboundSubs map[string]chan InboundEvent // worker SSE writers, by sub id

	outboundLog  []json.RawMessage // uploaded client_event payloads, in order
	outboundSeq  int64
	outboundSubs map[string]chan []byte // controller subscribers, by sub id

	internalLog []internalRow // transcript lane (resume hydration)
}

type InboundEvent struct {
	Seq     int64           `json:"seq"`
	EventID string          `json:"-"`
	Frame   json.RawMessage `json:"-"` // full SSE frame bytes
}

type internalRow struct {
	Payload json.RawMessage
}

// SessionInfo is the client-facing summary of a session.
type SessionInfo struct {
	ID       string          `json:"id"`
	Epoch    int64           `json:"epoch"`
	Dead     bool            `json:"dead"`
	State    json.RawMessage `json:"worker_state,omitempty"`
	LastPost string          `json:"last_heartbeat,omitempty"`
}

func newSession(id string) *Session {
	s := &Session{
		ID:           id,
		Token:        "ccr_" + randomHex(24),
		inboundSubs:  map[string]chan InboundEvent{},
		outboundSubs: map[string]chan []byte{},
	}
	return s
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// newEventID mints an id for a client_event frame. The protocol only requires
// a string; delivery acks echo it back.
func newEventID() string { return "evt_" + randomHex(12) }

// ---- worker lane ----

// RegisterWorker answers POST /worker/register.
func (s *Session) RegisterWorker() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	return s.epoch
}

// Epoch returns the current worker epoch.
func (s *Session) Epoch() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// SaveWorkerState stores the latest PUT /worker body.
func (s *Session) SaveWorkerState(body json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastState = body
}

// WorkerState returns the latest PUT /worker body.
func (s *Session) WorkerState() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastState
}

// PushInbound sends an SDK message (or any frame payload) to the worker over
// its SSE stream, assigning it a sequence number and event id. The frame is
// retained so a reconnecting worker can replay from from_sequence_num.
func (s *Session) PushInbound(payload json.RawMessage, eventType string) {
	if strings.TrimSpace(string(payload)) == "" {
		return
	}
	frame := clientEventFrame(payload, eventType)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.inboundSeq++
	ev := InboundEvent{Seq: s.inboundSeq, EventID: newEventID(), Frame: frame}
	ev.Frame = stampFrame(ev.Frame, ev.EventID, ev.Seq)
	s.inboundLog = append(s.inboundLog, ev)
	if len(s.inboundLog) > 4096 {
		s.inboundLog = s.inboundLog[len(s.inboundLog)-2048:]
	}
	for _, ch := range s.inboundSubs {
		select {
		case ch <- ev:
		default: // worker subscriber wedged; drop frame (it replays on reconnect)
		}
	}
}

// InboundSince returns frames with a sequence greater than from.
func (s *Session) InboundSince(from int64) []InboundEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []InboundEvent
	for _, ev := range s.inboundLog {
		if ev.Seq > from {
			out = append(out, ev)
		}
	}
	return out
}

// SubInbound subscribes the worker SSE stream; it returns the starting
// sequence number and a channel of frames. done unsubscribes.
func (s *Session) SubInbound() (ch chan InboundEvent, done func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := randomHex(8)
	ch = make(chan InboundEvent, 256)
	s.inboundSubs[id] = ch
	done = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.inboundSubs, id)
	}
	return ch, done
}

// HasWorker reports whether a worker SSE stream is currently attached.
func (s *Session) HasWorker() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inboundSubs) > 0
}

// ---- upload lane (worker → controllers) ----

// RecordUpload appends uploaded client_event payloads and fans them out to
// controller subscribers.
func (s *Session) RecordUpload(events []json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		if len(e) == 0 {
			continue
		}
		s.outboundSeq++
		s.outboundLog = append(s.outboundLog, e)
		b, _ := json.Marshal(e)
		for _, ch := range s.outboundSubs {
			select {
			case ch <- b:
			default:
			}
		}
	}
	if len(s.outboundLog) > 8192 {
		s.outboundLog = s.outboundLog[len(s.outboundLog)-4096:]
	}
}

// UploadsSince returns uploaded payloads after the given index (0-based).
func (s *Session) UploadsSince(after int64) ([]json.RawMessage, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if after < 0 {
		after = 0
	}
	if after >= int64(len(s.outboundLog)) {
		return nil, int64(len(s.outboundLog))
	}
	out := append([]json.RawMessage(nil), s.outboundLog[after:]...)
	return out, int64(len(s.outboundLog))
}

// SubUploads subscribes a controller feed. done unsubscribes.
func (s *Session) SubUploads() (ch chan []byte, done func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := randomHex(8)
	ch = make(chan []byte, 512)
	s.outboundSubs[id] = ch
	done = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.outboundSubs, id)
	}
	return ch, done
}

// ---- transcript lane ----

// RecordInternal appends transcript rows for resume hydration.
func (s *Session) RecordInternal(rows []json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.internalLog = append(s.internalLog, internalRows(rows)...)
	if len(s.internalLog) > 16384 {
		s.internalLog = s.internalLog[len(s.internalLog)-8192:]
	}
}

func internalRows(rows []json.RawMessage) []internalRow {
	out := make([]internalRow, 0, len(rows))
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		out = append(out, internalRow{Payload: r})
	}
	return out
}

// InternalPage returns a page of transcript rows. Pagination is cursor-based
// over a stable index; nextCursor is empty on the last page.
func (s *Session) InternalPage(cursor int64, subagents bool, agentID string) (rows []json.RawMessage, next int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	skipSubagents := !subagents && agentID == ""
	for i := int(cursor); i < len(s.internalLog); i++ {
		p := s.internalLog[i].Payload
		if skipSubagents && hasAgentLane(p) {
			continue
		}
		rows = append(rows, p)
		if len(rows) >= 1000 {
			return rows, int64(i + 1)
		}
	}
	return rows, -1
}

func hasAgentLane(p json.RawMessage) bool {
	var probe struct {
		SessionAgentID string `json:"session_agent_id"`
	}
	_ = json.Unmarshal(p, &probe)
	return probe.SessionAgentID != ""
}

// MarkDead flags the session as ended (worker exited or was killed).
func (s *Session) MarkDead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dead = true
	for id, ch := range s.inboundSubs {
		close(ch)
		delete(s.inboundSubs, id)
	}
	for id, ch := range s.outboundSubs {
		close(ch)
		delete(s.outboundSubs, id)
	}
}

// Info returns the client-facing session summary.
func (s *Session) Info() SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionInfo{ID: s.ID, Epoch: s.epoch, Dead: s.dead, State: s.lastState}
}

// ---- frame helpers ----

// clientEventFrame wraps an SDK payload in the SSE data envelope.
func clientEventFrame(payload json.RawMessage, eventType string) []byte {
	if eventType == "" {
		eventType = payloadType(payload)
	}
	env := map[string]any{
		"payload":      json.RawMessage(payload),
		"event_id":     "",
		"event_type":   eventType,
		"sequence_num": 0,
		"source":       "client",
	}
	b, _ := json.Marshal(env)
	return b
}

// stampFrame rewrites event_id and sequence_num into an already-encoded frame.
func stampFrame(frame []byte, eventID string, seq int64) []byte {
	var env map[string]any
	if err := json.Unmarshal(frame, &env); err != nil {
		return frame
	}
	env["event_id"] = eventID
	env["sequence_num"] = seq
	b, err := json.Marshal(env)
	if err != nil {
		return frame
	}
	return b
}

func payloadType(payload json.RawMessage) string {
	var probe struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(payload, &probe)
	return probe.Type
}

func sseFrame(event string, id int64, data []byte) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "event: %s\n", event)
	if id > 0 {
		fmt.Fprintf(&b, "id: %d\n", id)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fmt.Fprintf(&b, "data: %s\n", line)
	}
	b.WriteString("\n")
	return []byte(b.String())
}
