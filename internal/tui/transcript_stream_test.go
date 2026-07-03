package tui

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestApplyDelta(t *testing.T) {
	have := []claudecode.Chunk{{ID: "0"}, {ID: "1"}}
	// from_index 1 replaces chunk 1 and appends chunk 2
	d := api.TranscriptDelta{FromIndex: 1, Chunks: []claudecode.Chunk{{ID: "1", Text: "grown"}, {ID: "2"}}}
	got := applyDelta(have, d)
	if len(got) != 3 || got[1].Text != "grown" || got[2].ID != "2" {
		t.Fatalf("applyDelta = %+v", got)
	}
}

func TestApplyDeltaFromZeroReplacesAll(t *testing.T) {
	have := []claudecode.Chunk{{ID: "0"}, {ID: "1"}}
	d := api.TranscriptDelta{FromIndex: 0, Chunks: []claudecode.Chunk{{ID: "0"}}}
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
		"s1": {ID: "s1", ClaudeSessionID: "c1"},
		"s2": {ID: "s2"}, // no claude id yet
	}
	if got := m.cacheKeyFor("s1"); got != "c1" {
		t.Errorf("cacheKeyFor(s1) = %q, want c1", got)
	}
	if got := m.cacheKeyFor("s2"); got != "s2" {
		t.Errorf("cacheKeyFor(s2) = %q, want s2 (fallback)", got)
	}
}

// A /clear swaps the open session's ClaudeSessionID: re-subscribe onto the fresh
// (empty) cache key so no pre-clear chunks survive.
func TestResubscribeOnClearResetsToFreshStream(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 10
	m.mode = modeSession
	m.selectedID = "s1"
	prev := session.Session{ID: "s1", ClaudeSessionID: "c0"}
	cur := session.Session{ID: "s1", ClaudeSessionID: "c1"}
	m.sessions = map[string]session.Session{"s1": cur}
	m.activeSub = subRef{subID: "x", sessionID: "s1", cacheKey: "c0"}
	m.transcriptCache = map[string]cachedTranscript{"c0": {chunks: userChunks(20)}}
	m.transcript.chunks = userChunks(20)

	cmd := m.resubscribeOnClear(prev, true, cur)
	if cmd == nil {
		t.Fatal("ClaudeSessionID change should trigger a re-subscribe")
	}
	if m.activeSub.subID == "x" {
		t.Error("re-subscribe should mint a new sub id")
	}
	if m.activeSub.cacheKey != "c1" {
		t.Errorf("activeSub cacheKey = %q, want c1", m.activeSub.cacheKey)
	}
	if len(m.transcript.chunks) != 0 {
		t.Errorf("stale pre-clear chunks should be dropped, got %d", len(m.transcript.chunks))
	}
	if _, ok := m.transcriptCache["c0"]; ok {
		t.Error("superseded cache entry should be evicted")
	}
}

func TestResubscribeOnClearNoopBranches(t *testing.T) {
	base := func() *model {
		m := testModel()
		m.width, m.height = 80, 10
		m.mode = modeSession
		m.selectedID = "s1"
		m.activeSub = subRef{subID: "x", sessionID: "s1", cacheKey: "c0"}
		return &m
	}
	prev := session.Session{ID: "s1", ClaudeSessionID: "c0"}
	cur := session.Session{ID: "s1", ClaudeSessionID: "c1"}

	// Viewing a different session (or the list): leave the stream alone.
	m := base()
	m.selectedID = "s2"
	if cmd := m.resubscribeOnClear(prev, true, cur); cmd != nil {
		t.Error("must not re-subscribe when not viewing this session")
	}

	// Session new to us (no prior state): nothing to reset.
	m = base()
	if cmd := m.resubscribeOnClear(session.Session{}, false, cur); cmd != nil {
		t.Error("must not re-subscribe for a brand-new session")
	}

	// Current id unknown yet (pre-hook): can't key a fresh stream.
	m = base()
	if cmd := m.resubscribeOnClear(prev, true, session.Session{ID: "s1"}); cmd != nil {
		t.Error("must not re-subscribe when the new ClaudeSessionID is empty")
	}
}

func TestResubscribeOnClearNoop(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 10
	m.mode = modeSession
	m.selectedID = "s1"
	s := session.Session{ID: "s1", ClaudeSessionID: "c0"}
	m.sessions = map[string]session.Session{"s1": s}
	m.activeSub = subRef{subID: "x", sessionID: "s1", cacheKey: "c0"}
	if cmd := m.resubscribeOnClear(s, true, s); cmd != nil {
		t.Error("unchanged ClaudeSessionID must not re-subscribe")
	}
	// Drilled into a subagent: leave the stream alone.
	m.activeSub = subRef{subID: "x", sessionID: "s1", agentID: "a1", cacheKey: "c0"}
	cur := session.Session{ID: "s1", ClaudeSessionID: "c1"}
	m.sessions["s1"] = cur
	if cmd := m.resubscribeOnClear(s, true, cur); cmd != nil {
		t.Error("must not re-subscribe while drilled into a subagent")
	}
}
