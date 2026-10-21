package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type skillsDaemon struct {
	st *daemonState

	mu    sync.Mutex
	calls map[string]int
}

func (s *skillsDaemon) handle(method string, params json.RawMessage) (any, *rpcError) {
	s.mu.Lock()
	s.calls[method]++
	s.mu.Unlock()
	if method == "skills/list" {
		return map[string]any{"data": []map[string]any{{
			"cwd":    "/w",
			"errors": []any{},
			"skills": []map[string]any{
				{"name": "a", "description": "A long", "shortDescription": "A", "enabled": true, "path": "/s/a/SKILL.md", "scope": "user"},
				{"name": "b", "description": "B", "enabled": false, "path": "/s/b/SKILL.md", "scope": "user"},
				{"name": "v1.2", "description": "V", "enabled": true, "path": "/s/v/SKILL.md", "scope": "user"},
			},
		}}}, nil
	}
	return s.st.handle(method, params)
}

func (s *skillsDaemon) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

func startSkillsAdapter(t *testing.T) (*cxAdapter, *fakeDaemon, *skillsDaemon, session.Session) {
	t.Helper()
	sd := &skillsDaemon{
		st:    &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "idle")}},
		calls: map[string]int{},
	}
	f := newFakeDaemon(t, sd.handle)
	reg := registry.New()
	t.Setenv("CODEX_HOME", t.TempDir())
	a := &cxAdapter{}
	d := a.NewDiscoverer(reg, nil).(*discoverer)
	d.sockPath = func() (string, error) { return f.sock, nil }
	d.retry = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.ctx = ctx
	if err := d.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	waitSession(t, reg, "t1", func(session.Session) bool { return true })
	sess := session.Session{ID: "s1", Agent: Agent, AgentSessionID: "t1", Cwd: "/w", Status: session.StatusIdle}
	return a, f, sd, sess
}

func turnInput(t *testing.T, m wireMsg) []map[string]any {
	t.Helper()
	var p struct {
		ThreadID string           `json:"threadId"`
		Input    []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		t.Fatal(err)
	}
	if p.ThreadID != "t1" {
		t.Fatalf("threadId = %q", p.ThreadID)
	}
	return p.Input
}

var skillA = map[string]any{"type": "skill", "name": "a", "path": "/s/a/SKILL.md"}

func TestCompletePromptSlashIsCompactOnly(t *testing.T) {
	a := &cxAdapter{}
	got, err := a.CompletePrompt(context.Background(), session.Session{ID: "s1", Agent: Agent}, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "compact" || got[0].Description == "" {
		t.Fatalf("items = %+v", got)
	}
}

func TestCompletePromptDollarListsEnabledSkills(t *testing.T) {
	a, f, _, sess := startSkillsAdapter(t)
	got, err := a.CompletePrompt(context.Background(), sess, "$")
	if err != nil {
		t.Fatal(err)
	}
	want := []api.PromptCompletion{{Name: "a", Description: "A"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %+v, want %+v", got, want)
	}
	var p struct {
		Cwds        []string `json:"cwds"`
		ForceReload bool     `json:"forceReload"`
	}
	if err := json.Unmarshal(f.expect(t, "skills/list").Params, &p); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Cwds, []string{"/w"}) || !p.ForceReload {
		t.Fatalf("params = %+v", p)
	}
}

func TestSkillDescriptionPrefersInterfaceShortDescription(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"description":"D","shortDescription":"S","interface":{"shortDescription":"I"}}`, "I"},
		{`{"description":"D","shortDescription":"S","interface":{}}`, "S"},
		{`{"description":"D"}`, "D"},
	}
	for _, c := range cases {
		var m skillMetadata
		if err := json.Unmarshal([]byte(c.raw), &m); err != nil {
			t.Fatal(err)
		}
		if got := m.summary(); got != c.want {
			t.Errorf("%s: summary = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestSendPromptCompactRunsThreadCompact(t *testing.T) {
	a, f, sd, sess := startSkillsAdapter(t)
	if err := a.SendPrompt(context.Background(), sess, " /compact "); err != nil {
		t.Fatal(err)
	}
	if m := f.expect(t, "thread/compact/start"); string(m.Params) != `{"threadId":"t1"}` {
		t.Fatalf("thread/compact/start params = %s", m.Params)
	}
	if n := sd.count("turn/start"); n != 0 {
		t.Fatalf("turn/start calls = %d", n)
	}
}

func TestSendPromptSkillMentionAddsSkillItem(t *testing.T) {
	a, f, sd, sess := startSkillsAdapter(t)
	if err := a.SendPrompt(context.Background(), sess, "$a"); err != nil {
		t.Fatal(err)
	}
	got := turnInput(t, f.expect(t, "turn/start"))
	want := []map[string]any{{"type": "text", "text": "$a"}, skillA}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("input = %v, want %v", got, want)
	}
	if n := sd.count("skills/list"); n != 1 {
		t.Fatalf("skills/list calls = %d, want 1", n)
	}
}

func TestSendPromptSkillMentionsAreDistinctAndKnown(t *testing.T) {
	a, f, _, sess := startSkillsAdapter(t)
	text := "$a, then $a again; $b is off, x$a and $zz are not"
	if err := a.SendPrompt(context.Background(), sess, text); err != nil {
		t.Fatal(err)
	}
	got := turnInput(t, f.expect(t, "turn/start"))
	want := []map[string]any{{"type": "text", "text": text}, skillA}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("input = %v, want %v", got, want)
	}
}

func TestSendPromptPlainSlashTextIsPrompt(t *testing.T) {
	a, f, sd, sess := startSkillsAdapter(t)
	if err := a.SendPrompt(context.Background(), sess, "/tmp/x is broken"); err != nil {
		t.Fatal(err)
	}
	got := turnInput(t, f.expect(t, "turn/start"))
	want := []map[string]any{{"type": "text", "text": "/tmp/x is broken"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("input = %v, want %v", got, want)
	}
	if n := sd.count("turn/start"); n != 1 {
		t.Fatalf("turn/start calls = %d, want 1", n)
	}
	if n := sd.count("thread/compact/start") + sd.count("skills/list"); n != 0 {
		t.Fatalf("unexpected compact/skills calls = %d", n)
	}
}

func TestSendPromptPriceIsNoMention(t *testing.T) {
	a, f, sd, sess := startSkillsAdapter(t)
	if err := a.SendPrompt(context.Background(), sess, "it costs $5"); err != nil {
		t.Fatal(err)
	}
	f.expect(t, "turn/start")
	if n := sd.count("skills/list"); n != 0 {
		t.Fatalf("skills/list calls = %d, want 0", n)
	}
}
