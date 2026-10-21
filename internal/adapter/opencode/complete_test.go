package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type ocRequest struct {
	method, path, dir, body string
}

type completeServer struct {
	commands      []map[string]any
	skills        []map[string]any
	emptyCommands int
	commandsFail  bool

	mu   sync.Mutex
	reqs []ocRequest
}

func (s *completeServer) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, ocRequest{r.Method, r.URL.Path, r.URL.Query().Get("location[directory]"), string(b)})
	s.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/command":
		s.mu.Lock()
		commands := s.commands
		if s.emptyCommands > 0 {
			s.emptyCommands--
			commands = []map[string]any{}
		}
		s.mu.Unlock()
		if s.commandsFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"location": map[string]string{"directory": "/w"}, "data": commands})
	case r.Method == http.MethodGet && r.URL.Path == "/api/skill":
		_ = json.NewEncoder(w).Encode(map[string]any{"location": map[string]string{"directory": "/w"}, "data": s.skills})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/command"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost:
		_, _ = w.Write([]byte(`{"data":{}}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *completeServer) requests() []ocRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ocRequest(nil), s.reqs...)
}

func startCompleteAdapter(t *testing.T, s *completeServer) (*ocAdapter, session.Session) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	a := &ocAdapter{disc: newTestDiscoverer(registry.New(), newClient(serviceInfo{URL: srv.URL}))}
	sess := session.Session{ID: "s1", Agent: Agent, AgentSessionID: "ses_1", Cwd: "/w", Status: session.StatusIdle}
	return a, sess
}

func testCommands() []map[string]any {
	return []map[string]any{
		{"name": "init", "description": "create AGENTS.md"},
		{"name": "review", "description": "review changes"},
	}
}

func testSkills() []map[string]any {
	return []map[string]any{
		{"id": "tdd", "name": "test-driven-development", "description": "d", "path": "/s/tdd/SKILL.md", "content": "x"},
		{"id": "v1.2", "name": "v1.2", "description": "v", "path": "/s/v/SKILL.md", "content": "x"},
	}
}

func TestCompletePromptSlashMergesCompactAndCommands(t *testing.T) {
	s := &completeServer{commands: testCommands()}
	a, sess := startCompleteAdapter(t, s)
	got, err := a.CompletePrompt(context.Background(), sess, "/")
	if err != nil {
		t.Fatal(err)
	}
	reqs := s.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodGet || reqs[0].path != "/api/command" || reqs[0].dir != "/w" {
		t.Fatalf("requests = %+v", reqs)
	}
	names := make([]string, 0, len(got))
	for _, it := range got {
		names = append(names, it.Name)
	}
	if !reflect.DeepEqual(names, []string{"compact", "init", "review"}) {
		t.Fatalf("names = %v", names)
	}
	if got[2].Description != "review changes" {
		t.Fatalf("description = %q", got[2].Description)
	}
}

func TestCompletePromptSlashRetriesEmptyListing(t *testing.T) {
	s := &completeServer{commands: testCommands(), emptyCommands: 2}
	a, sess := startCompleteAdapter(t, s)
	got, err := a.CompletePrompt(context.Background(), sess, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || len(s.requests()) != 3 {
		t.Fatalf("items = %+v, requests = %+v", got, s.requests())
	}
}

func TestCompletePromptSlashFailsWithListing(t *testing.T) {
	s := &completeServer{commandsFail: true}
	a, sess := startCompleteAdapter(t, s)
	if got, err := a.CompletePrompt(context.Background(), sess, "/"); err == nil {
		t.Fatalf("items = %+v, want an error", got)
	}
}

func TestCompletePromptAtListsSkills(t *testing.T) {
	s := &completeServer{skills: testSkills()}
	a, sess := startCompleteAdapter(t, s)
	got, err := a.CompletePrompt(context.Background(), sess, "@")
	if err != nil {
		t.Fatal(err)
	}
	want := []api.PromptCompletion{{Name: "tdd", Description: "d"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %+v, want %+v", got, want)
	}
	reqs := s.requests()
	if len(reqs) != 1 || reqs[0].path != "/api/skill" || reqs[0].dir != "/w" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestSendPromptCompactPostsCompact(t *testing.T) {
	s := &completeServer{commands: testCommands()}
	a, sess := startCompleteAdapter(t, s)
	if err := a.SendPrompt(context.Background(), sess, "/compact"); err != nil {
		t.Fatal(err)
	}
	reqs := s.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPost || reqs[0].path != "/api/session/ses_1/compact" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func assertJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("body %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestSendPromptUnknownSlashIsPrompt(t *testing.T) {
	s := &completeServer{commands: testCommands()}
	a, sess := startCompleteAdapter(t, s)
	if err := a.SendPrompt(context.Background(), sess, "/tmp/x is broken"); err != nil {
		t.Fatal(err)
	}
	reqs := s.requests()
	last := reqs[len(reqs)-1]
	if last.method != http.MethodPost || last.path != "/api/session/ses_1/prompt" {
		t.Fatalf("requests = %+v", reqs)
	}
	assertJSON(t, last.body, `{"text":"/tmp/x is broken"}`)
}

func TestSendPromptSkillMentionAddsSkillPart(t *testing.T) {
	s := &completeServer{skills: testSkills()}
	a, sess := startCompleteAdapter(t, s)
	if err := a.SendPrompt(context.Background(), sess, "é @tdd go"); err != nil {
		t.Fatal(err)
	}
	reqs := s.requests()
	if len(reqs) != 2 || reqs[0].path != "/api/skill" || reqs[1].path != "/api/session/ses_1/prompt" {
		t.Fatalf("requests = %+v", reqs)
	}
	assertJSON(t, reqs[1].body, `{"text":"é @tdd go","skills":[{"id":"tdd","mention":{"start":2,"end":6,"text":"@tdd"}}]}`)
}

func TestSendPromptKnownCommandPostsCommand(t *testing.T) {
	s := &completeServer{commands: testCommands()}
	a, sess := startCompleteAdapter(t, s)
	if err := a.SendPrompt(context.Background(), sess, "/review  my branch "); err != nil {
		t.Fatal(err)
	}
	reqs := s.requests()
	if len(reqs) != 2 || reqs[0].path != "/api/command" || reqs[1].path != "/api/session/ses_1/command" {
		t.Fatalf("requests = %+v", reqs)
	}
	assertJSON(t, reqs[1].body, `{"name":"review","text":"my branch"}`)
}

func TestSendPromptUnknownSlashKeepsSkillMentions(t *testing.T) {
	s := &completeServer{commands: testCommands(), skills: testSkills()}
	a, sess := startCompleteAdapter(t, s)
	if err := a.SendPrompt(context.Background(), sess, "/tmp/x @tdd"); err != nil {
		t.Fatal(err)
	}
	reqs := s.requests()
	last := reqs[len(reqs)-1]
	if last.path != "/api/session/ses_1/prompt" {
		t.Fatalf("requests = %+v", reqs)
	}
	assertJSON(t, last.body, `{"text":"/tmp/x @tdd","skills":[{"id":"tdd","mention":{"start":7,"end":11,"text":"@tdd"}}]}`)
}

func TestSendPromptPlainSkipsListing(t *testing.T) {
	s := &completeServer{commands: testCommands(), skills: testSkills()}
	a, sess := startCompleteAdapter(t, s)
	if err := a.SendPrompt(context.Background(), sess, "mail me@example.com"); err != nil {
		t.Fatal(err)
	}
	reqs := s.requests()
	if len(reqs) != 1 || reqs[0].path != "/api/session/ses_1/prompt" {
		t.Fatalf("requests = %+v", reqs)
	}
	assertJSON(t, reqs[0].body, `{"text":"mail me@example.com"}`)
}
