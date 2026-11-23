package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/memory"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type fakeTranscriptAdapter struct {
	adapter.Adapter
	mu      sync.Mutex
	entries []transcript.Entry
}

func (f *fakeTranscriptAdapter) ReadTranscriptView(string) (transcript.TranscriptView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return transcript.TranscriptView{Entries: append([]transcript.Entry(nil), f.entries...)}, nil
}

func (f *fakeTranscriptAdapter) set(es ...transcript.Entry) {
	f.mu.Lock()
	f.entries = es
	f.mu.Unlock()
}

type stubExtractor struct {
	reply   string
	err     error
	prompts []string
}

func (s *stubExtractor) Extract(_ context.Context, prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	return s.reply, s.err
}

func uEntry(id, text string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntryUser, Text: text}
}
func aEntry(id, text string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntryText, Text: text}
}

type captureRig struct {
	*threadRig
	ad     *fakeTranscriptAdapter
	ex     *stubExtractor
	thread string
	sess   string
	clock  time.Time
}

func newCaptureRig(t *testing.T) *captureRig {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	tr := newThreadRig(t)
	tr.d.pagentCfg.Defaults.Memory.CaptureAfter = 2 * time.Minute
	r := &captureRig{threadRig: tr, ad: &fakeTranscriptAdapter{}, ex: &stubExtractor{reply: `{"ops":[],"log":""}`}}
	tr.d.adapters = map[string]adapter.Adapter{"claude": r.ad}
	tr.d.extractorFor = func(pagent.Settings) memory.Extractor { return r.ex }
	res, err := tr.send(t, api.PAgentSendParams{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	r.thread, r.sess = res.ThreadID, res.SessionID
	s, _ := tr.d.reg.Get(r.sess)
	if err := tr.store.SetAgentSessionID(context.Background(), r.sess, "claude", s.AgentSessionID, "/tmp/transcript.jsonl"); err != nil {
		t.Fatal(err)
	}
	r.setNow(3 * time.Minute)
	return r
}

// setNow sets the node clock to d after the session became idle.
func (r *captureRig) setNow(d time.Duration) {
	r.clock = time.Now().Add(d)
	r.d.pagentNow = func() time.Time { return r.clock }
}

// newIdlePeriod makes the last capture older than the session's idleness, as
// when a new turn has finished since.
func (r *captureRig) newIdlePeriod(t *testing.T) {
	t.Helper()
	c := r.cursor(t)
	c.UpdatedAt = time.Now().Add(-time.Hour)
	if err := r.store.SetCaptureCursor(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func (r *captureRig) capture() { r.d.pagentCapture(context.Background()) }

func (r *captureRig) memoryFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.agent.Dir, rel))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func (r *captureRig) cursor(t *testing.T) pagent.CaptureCursor {
	t.Helper()
	c, _, err := r.store.CaptureCursor(context.Background(), r.thread, r.sess)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (r *captureRig) errorEvents(t *testing.T) int {
	n := 0
	for _, e := range r.events(t, r.thread) {
		if e.Kind == evMemoryError {
			n++
		}
	}
	return n
}

func TestCaptureAppliesAfterQuiet(t *testing.T) {
	r := newCaptureRig(t)
	r.ad.set(uEntry("u1", "I prefer tea"), aEntry("a1", "noted"))
	r.ex.reply = `{"ops":[{"action":"add","file":"MEMORY.md","text":"Deploys go through staging"}],"log":"discussed tea"}`
	r.setNow(time.Minute)
	r.capture()
	if len(r.ex.prompts) != 0 {
		t.Fatal("captured before capture-after")
	}
	r.setNow(3 * time.Minute)
	r.capture()
	if len(r.ex.prompts) != 1 || !strings.Contains(r.ex.prompts[0], "User: I prefer tea") {
		t.Fatalf("prompts = %v", r.ex.prompts)
	}
	if got := r.memoryFile(t, "memory/MEMORY.md"); !strings.Contains(got, "Deploys go through staging") {
		t.Fatalf("MEMORY.md = %q", got)
	}
	if c := r.cursor(t); c.EntryCount != 2 || c.LastEntryID != "a1" {
		t.Fatalf("cursor = %+v", c)
	}
	r.capture()
	if len(r.ex.prompts) != 1 {
		t.Fatal("captured again with nothing new")
	}
}

func TestCaptureDropsUnquotedUserFacts(t *testing.T) {
	r := newCaptureRig(t)
	r.ad.set(uEntry("u1", "I really like   Go"), aEntry("a1", "ok"))
	r.ex.reply = `{"ops":[
	  {"action":"add","file":"USER.md","text":"Likes Go","quote":"i really like go"},
	  {"action":"add","file":"USER.md","text":"Lives in Paris"},
	  {"action":"add","file":"MEMORY.md","text":"Invented","quote":"I live in Paris"},
	  {"action":"add","file":"MEMORY.md","text":"Project uses sqlc"}
	],"log":""}`
	r.capture()
	user, mem := r.memoryFile(t, "memory/USER.md"), r.memoryFile(t, "memory/MEMORY.md")
	if !strings.Contains(user, "Likes Go") || strings.Contains(user, "Paris") {
		t.Fatalf("USER.md = %q", user)
	}
	if strings.Contains(mem, "Invented") || !strings.Contains(mem, "Project uses sqlc") {
		t.Fatalf("MEMORY.md = %q", mem)
	}
}

func TestCaptureCursorResetOnShrink(t *testing.T) {
	r := newCaptureRig(t)
	r.ad.set(uEntry("u1", "one"), aEntry("a1", "two"), uEntry("u2", "three"))
	r.capture()
	if c := r.cursor(t); c.EntryCount != 3 {
		t.Fatalf("cursor = %+v", c)
	}
	// The transcript is rewritten shorter, then with a different entry at the cursor.
	r.ad.set(uEntry("x1", "fresh"))
	r.newIdlePeriod(t)
	r.capture()
	last := r.ex.prompts[len(r.ex.prompts)-1]
	if len(r.ex.prompts) != 2 || !strings.Contains(last, "User: fresh") {
		t.Fatalf("prompts = %v", r.ex.prompts)
	}
	if c := r.cursor(t); c.EntryCount != 1 || c.LastEntryID != "x1" {
		t.Fatalf("cursor = %+v", c)
	}

	r.ad.set(uEntry("y1", "other"), uEntry("y2", "again"))
	r.newIdlePeriod(t)
	r.capture()
	last = r.ex.prompts[len(r.ex.prompts)-1]
	if !strings.Contains(last, "User: other") || !strings.Contains(last, "User: again") {
		t.Fatalf("id mismatch did not reset: %s", last)
	}
}

func TestCaptureOnlyNewEntries(t *testing.T) {
	r := newCaptureRig(t)
	r.ad.set(uEntry("u1", "first"), aEntry("a1", "reply"))
	r.capture()
	r.ad.set(uEntry("u1", "first"), aEntry("a1", "reply"), uEntry("u2", "second"))
	r.newIdlePeriod(t)
	r.capture()
	last := r.ex.prompts[len(r.ex.prompts)-1]
	if len(r.ex.prompts) != 2 || strings.Contains(last, "User: first") || !strings.Contains(last, "User: second") {
		t.Fatalf("prompts = %v", r.ex.prompts)
	}
}

func TestCaptureSkipsRunThreads(t *testing.T) {
	r := newCaptureRig(t)
	ctx := context.Background()
	for _, th := range []pagent.Thread{
		{ID: "th_run", AgentID: r.agent.ID, Kind: "run", StartedBy: "you"},
		{ID: "th_routine", AgentID: r.agent.ID, Kind: "chat", StartedBy: "routine"},
	} {
		if err := r.store.InsertThread(ctx, th); err != nil {
			t.Fatal(err)
		}
		sid := r.register("claude", "as-"+th.ID)
		if err := r.store.InsertThreadSession(ctx, pagent.ThreadSession{ThreadID: th.ID, SessionID: sid, CLI: "claude", StartedBy: "you"}); err != nil {
			t.Fatal(err)
		}
		if err := r.store.SetAgentSessionID(ctx, sid, "claude", "as-"+th.ID, "/tmp/"+th.ID); err != nil {
			t.Fatal(err)
		}
	}
	r.ad.set(uEntry("u1", "hello"))
	r.capture()
	if len(r.ex.prompts) != 1 {
		t.Fatalf("extractions = %d, want only the chat thread", len(r.ex.prompts))
	}
	for _, id := range []string{"th_run", "th_routine"} {
		if _, ok, _ := r.store.CaptureCursor(ctx, id, "x"); ok {
			t.Fatal("cursor for skipped thread")
		}
	}
}

func TestCaptureExtractorErrorNoChange(t *testing.T) {
	r := newCaptureRig(t)
	r.ad.set(uEntry("u1", "hello"))
	r.ex.err = errors.New("boom")
	r.capture()
	r.capture()
	r.setNow(30 * time.Minute)
	r.capture()
	if len(r.ex.prompts) != 1 {
		t.Fatalf("extractor called %d times within the hour", len(r.ex.prompts))
	}
	if _, ok, _ := r.store.CaptureCursor(context.Background(), r.thread, r.sess); ok {
		t.Fatal("cursor moved after error")
	}
	if got := r.memoryFile(t, "memory/MEMORY.md"); strings.Contains(got, "boom") {
		t.Fatal("memory changed")
	}
	if n := r.errorEvents(t); n != 1 {
		t.Fatalf("memory.error events = %d, want 1", n)
	}
	r.ex.err = nil
	r.setNow(2 * time.Hour)
	r.capture()
	if len(r.ex.prompts) != 2 || r.cursor(t).EntryCount != 1 {
		t.Fatalf("no retry after the hour: prompts = %d", len(r.ex.prompts))
	}
}

func TestCaptureNoExtractorEventOncePerHour(t *testing.T) {
	r := newCaptureRig(t)
	r.d.extractorFor = func(pagent.Settings) memory.Extractor { return nil }
	r.ad.set(uEntry("u1", "hello"))
	r.capture()
	r.capture()
	if n := r.errorEvents(t); n != 1 {
		t.Fatalf("memory.error events = %d", n)
	}
}

func TestCaptureEndedSession(t *testing.T) {
	r := newCaptureRig(t)
	r.ad.set(uEntry("u1", "bye"))
	r.setNow(0)
	r.end(r.sess)
	r.capture()
	if len(r.ex.prompts) != 1 {
		t.Fatalf("extractions after end = %d", len(r.ex.prompts))
	}
	r.capture()
	if len(r.ex.prompts) != 1 {
		t.Fatal("ended session captured twice")
	}
}

func TestCaptureModeOffAndBusy(t *testing.T) {
	r := newCaptureRig(t)
	r.ad.set(uEntry("u1", "hello"))
	r.setStatus(r.sess, session.StatusWorking, nil)
	r.capture()
	if len(r.ex.prompts) != 0 {
		t.Fatal("captured a working session")
	}
	r.setStatus(r.sess, session.StatusIdle, nil)
	r.d.pagentCfg.Defaults.Memory.Mode = "off"
	r.setNow(10 * time.Minute)
	r.capture()
	if len(r.ex.prompts) != 0 {
		t.Fatal("captured with memory off")
	}
}

func TestCaptureInvalidExtractorMessage(t *testing.T) {
	r := newCaptureRig(t)
	r.d.extractorFor = func(pagent.Settings) memory.Extractor { return nil }
	r.d.pagentCfg.Defaults.Memory.Extractor = "gemini"
	r.ad.set(uEntry("u1", "hello"))
	r.capture()
	var msg string
	for _, e := range r.events(t, r.thread) {
		if e.Kind == evMemoryError {
			msg = string(e.Payload)
		}
	}
	if !strings.Contains(msg, "invalid memory.extractor: gemini") {
		t.Fatalf("message = %s", msg)
	}
}

func TestCaptureRejectedOpsReportedOncePerHour(t *testing.T) {
	r := newCaptureRig(t)
	r.ex.reply = `{"ops":[{"action":"add","file":"MEMORY.md","text":"key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"}],"log":""}`
	r.ad.set(uEntry("u1", "hello"))
	r.capture()
	r.ad.set(uEntry("u1", "hello"), uEntry("u2", "again"))
	r.newIdlePeriod(t)
	r.capture()
	if n := r.errorEvents(t); n > 1 {
		t.Fatalf("memory.error events = %d", n)
	}
}

func TestCaptureResumedSessionContinues(t *testing.T) {
	r := newCaptureRig(t)
	ctx := context.Background()
	r.ad.set(uEntry("u1", "first"), aEntry("a1", "reply"))
	r.capture()
	if len(r.ex.prompts) != 1 {
		t.Fatalf("first capture: %d", len(r.ex.prompts))
	}
	// The session is resumed: a new session writes on in the same transcript.
	r.end(r.sess)
	if err := r.store.EndThreadSession(ctx, r.sess, "ended"); err != nil {
		t.Fatal(err)
	}
	sid := r.register("claude", "resumed")
	if err := r.store.InsertThreadSession(ctx, pagent.ThreadSession{ThreadID: r.thread, SessionID: sid, CLI: "claude", AgentSessionID: "resumed", TranscriptPath: "/tmp/transcript.jsonl", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	r.ad.set(uEntry("u1", "first"), aEntry("a1", "reply"), uEntry("u2", "second"))
	r.setNow(3 * time.Minute)
	r.capture()
	if len(r.ex.prompts) != 2 {
		t.Fatalf("extractions = %d: %v", len(r.ex.prompts), r.ex.prompts)
	}
	if last := r.ex.prompts[1]; strings.Contains(last, "User: first") || !strings.Contains(last, "User: second") {
		t.Fatalf("recaptured old entries: %s", last)
	}
	r.capture()
	if len(r.ex.prompts) != 2 {
		t.Fatalf("captured again: %d", len(r.ex.prompts))
	}
}
