package tui

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestApplyDelta(t *testing.T) {
	have := []transcript.Entry{{ID: "0"}, {ID: "1"}}
	// from_index 1 replaces chunk 1 and appends chunk 2
	d := api.TranscriptDelta{FromIndex: 1, Entries: []transcript.Entry{{ID: "1", Text: "grown"}, {ID: "2"}}}
	got := applyDelta(have, d)
	if len(got) != 3 || got[1].Text != "grown" || got[2].ID != "2" {
		t.Fatalf("applyDelta = %+v", got)
	}
}

func TestApplyDeltaFromZeroReplacesAll(t *testing.T) {
	have := []transcript.Entry{{ID: "0"}, {ID: "1"}}
	d := api.TranscriptDelta{FromIndex: 0, Entries: []transcript.Entry{{ID: "0"}}}
	got := applyDelta(have, d)
	if len(got) != 1 {
		t.Fatalf("want full replace to 1 chunk, got %d", len(got))
	}
}

func TestNewSubIDUnique(t *testing.T) {
	if a, b := newSubID(), newSubID(); a == b || a == "" {
		t.Fatalf("sub ids not unique/non-empty: %q %q", a, b)
	}
}

func TestCacheKeyForPrefersClaudeID(t *testing.T) {
	m := testModel()
	m.sessions = map[string]session.Session{
		"s1": {ID: "s1", AgentSessionID: "c1"},
		"s2": {ID: "s2"}, // no claude id yet
	}
	if got := m.cacheKeyFor("s1"); got != "c1" {
		t.Errorf("cacheKeyFor(s1) = %q, want c1", got)
	}
	if got := m.cacheKeyFor("s2"); got != "s2" {
		t.Errorf("cacheKeyFor(s2) = %q, want s2 (fallback)", got)
	}
}

// A /clear swaps the open session's AgentSessionID: re-subscribe onto the fresh
// (empty) cache key so no pre-clear entries survive.
func TestResubscribeOnClearResetsToFreshStream(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 10
	m = withLive(m, "s1")
	prev := session.Session{ID: "s1", AgentSessionID: "c0"}
	cur := session.Session{ID: "s1", AgentSessionID: "c1"}
	m.sessions = map[string]session.Session{"s1": cur}
	m = withTr(m, func(t *transcriptComp) { t.activeSub = subRef{subID: "x", sessionID: "s1", cacheKey: "c0"} })
	m.transcriptCache = map[string]cachedTranscript{"c0": {entries: userEntries(20)}}
	m = withTr(m, func(t *transcriptComp) { t.transcript.entries = userEntries(20) })

	cmd := m.resubscribeOnClear(prev, true, cur)
	if cmd == nil {
		t.Fatal("AgentSessionID change should trigger a re-subscribe")
	}
	if trOf(m).activeSub.subID == "x" {
		t.Error("re-subscribe should mint a new sub id")
	}
	if trOf(m).activeSub.cacheKey != "c1" {
		t.Errorf("activeSub cacheKey = %q, want c1", trOf(m).activeSub.cacheKey)
	}
	if len(trOf(m).transcript.entries) != 0 {
		t.Errorf("stale pre-clear entries should be dropped, got %d", len(trOf(m).transcript.entries))
	}
	if _, ok := m.transcriptCache["c0"]; ok {
		t.Error("superseded cache entry should be evicted")
	}
}

func TestResubscribeOnClearNoopBranches(t *testing.T) {
	base := func(id string) *model {
		m := testModel()
		m.width, m.height = 80, 10
		m = withLive(m, id)
		m = withTr(m, func(t *transcriptComp) { t.activeSub = subRef{subID: "x", sessionID: id, cacheKey: "c0"} })
		return &m
	}
	prev := session.Session{ID: "s1", AgentSessionID: "c0"}
	cur := session.Session{ID: "s1", AgentSessionID: "c1"}

	// Viewing a different session (or the list): leave the stream alone.
	m := base("s2")
	if cmd := m.resubscribeOnClear(prev, true, cur); cmd != nil {
		t.Error("must not re-subscribe when not viewing this session")
	}

	// Session new to us (no prior state): nothing to reset.
	m = base("s1")
	if cmd := m.resubscribeOnClear(session.Session{}, false, cur); cmd != nil {
		t.Error("must not re-subscribe for a brand-new session")
	}

	// Current id unknown yet (pre-hook): can't key a fresh stream.
	m = base("s1")
	if cmd := m.resubscribeOnClear(prev, true, session.Session{ID: "s1"}); cmd != nil {
		t.Error("must not re-subscribe when the new AgentSessionID is empty")
	}
}

func TestResubscribeOnClearNoop(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 10
	m = withLive(m, "s1")
	s := session.Session{ID: "s1", AgentSessionID: "c0"}
	m.sessions = map[string]session.Session{"s1": s}
	m = withTr(m, func(t *transcriptComp) { t.activeSub = subRef{subID: "x", sessionID: "s1", cacheKey: "c0"} })
	if cmd := m.resubscribeOnClear(s, true, s); cmd != nil {
		t.Error("unchanged AgentSessionID must not re-subscribe")
	}
	// Drilled into a subagent: leave the stream alone.
	m = withTr(m, func(t *transcriptComp) {
		t.activeSub = subRef{subID: "x", sessionID: "s1", agentID: "a1", cacheKey: "c0"}
	})
	cur := session.Session{ID: "s1", AgentSessionID: "c1"}
	m.sessions["s1"] = cur
	if cmd := m.resubscribeOnClear(s, true, cur); cmd != nil {
		t.Error("must not re-subscribe while drilled into a subagent")
	}
}

func subscribeCalls(rc *recordingClient) []api.TranscriptSubscribeParams {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var out []api.TranscriptSubscribeParams
	for i, m := range rc.calls {
		if m == api.MethodTranscriptSubscribe {
			out = append(out, rc.params[i].(api.TranscriptSubscribeParams))
		}
	}
	return out
}

func sessionEventNote(typ registry.EventType, s session.Session) api.Notification {
	params, _ := json.Marshal(registry.Event{Type: typ, Session: s})
	return api.Notification{Method: api.MethodSessionEvent, Params: params}
}

// A restarted node drops its subscriptions while the gateway link stays up, so
// the open session coming back (online again, or re-listed after a gateway
// restart) must re-subscribe its live stream.
func TestSessionReturnResubscribesLiveStream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prev    *session.Session
		typ     registry.EventType
		resubOK bool
	}{
		{"back online", &session.Session{ID: "s1", Offline: true}, registry.EventUpdated, true},
		{"re-listed", nil, registry.EventAdded, true},
		{"already online", &session.Session{ID: "s1"}, registry.EventUpdated, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testModel()
			rc := &recordingClient{}
			m.client = rc
			m = withLive(m, "s1")
			m.sessions = map[string]session.Session{}
			if tc.prev != nil {
				m.sessions["s1"] = *tc.prev
			}
			m = withTr(m, func(t *transcriptComp) { t.activeSub = subRef{subID: "x", sessionID: "s1", cacheKey: "s1"} })
			m.transcriptCache = map[string]cachedTranscript{"s1": {entries: userEntries(3)}}
			m = withTr(m, func(t *transcriptComp) { t.transcript.entries = userEntries(3) })

			runCmd(m.applyEvent(sessionEventNote(tc.typ, session.Session{ID: "s1"})))
			subs := subscribeCalls(rc)
			if !tc.resubOK {
				if len(subs) != 0 {
					t.Fatalf("must not re-subscribe: %+v", subs)
				}
				return
			}
			if len(subs) != 1 || subs[0].SubID != "x" || subs[0].HaveEntries != 3 {
				t.Fatalf("want one re-subscribe of x with have_entries 3, got %+v", subs)
			}
		})
	}
}

// A failed re-subscribe (node not back yet) keeps the shown entries, so the next
// re-subscribe's have_entries matches what the view holds.
func TestSubscribeErrorKeepsEntries(t *testing.T) {
	m := testModel()
	m = withLive(m, "s1")
	m = withTr(m, func(t *transcriptComp) {
		t.activeSub = subRef{subID: "x", sessionID: "s1", cacheKey: "s1"}
		t.transcript.entries = userEntries(3)
	})
	m.transcriptCache = map[string]cachedTranscript{"s1": {entries: userEntries(3)}}
	m, _ = upd(m, transcriptMsg{id: "s1", err: errors.New("node offline")})
	if got := len(trOf(m).transcript.entries); got != 3 {
		t.Fatalf("entries after subscribe error = %d, want 3", got)
	}
	m, _ = upd(m, transcriptDeltaMsg{ref: trOf(m).activeSub, delta: api.TranscriptDelta{FromIndex: 3}, initial: true})
	if trOf(m).transcript.err != nil {
		t.Error("a successful re-subscribe should clear the error")
	}
}
