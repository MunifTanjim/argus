package node

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func modTestNode(t *testing.T) (*Node, *httptest.Server) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // ProcessHook reads ~/.claude/sessions
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	srv := httptest.NewServer(d.modHandler())
	t.Cleanup(srv.Close)
	return d, srv
}

func modPost(t *testing.T, ctx context.Context, url string, body, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("content-type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

var bashLs = map[string]any{"command": "ls"}

// askPermission registers a permission request for Claude session s1 and
// returns its request id and the registry session id.
func askPermission(t *testing.T, d *Node, srv *httptest.Server, toolName string, toolInput map[string]any) (string, string) {
	t.Helper()
	var out modEventResult
	modPost(t, context.Background(), srv.URL+"/claude/event", map[string]any{
		"event": "PermissionRequest",
		"payload": map[string]any{
			"session_id": "s1", "cwd": "/w", "tool_name": toolName, "tool_input": toolInput,
		},
	}, &out)
	if out.RequestID == "" {
		t.Fatalf("PermissionRequest answered %+v, want a requestId", out)
	}
	sid := registry.AgentSessionKey(claudecode.Agent, "s1")
	waitFor(t, func() bool { return parked(d, sid) })
	return out.RequestID, sid
}

// promptCleared reports that argus no longer waits on or shows the request.
func promptCleared(d *Node, sid string) bool {
	s, _ := d.reg.Get(sid)
	return !parked(d, sid) && s.Interaction == nil
}

func TestModEventRegistersSession(t *testing.T) {
	d, srv := modTestNode(t)
	code := modPost(t, context.Background(), srv.URL+"/claude/event", map[string]any{
		"event":   "SessionStart",
		"payload": map[string]any{"session_id": "s1", "cwd": "/w", "source": "startup"},
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	s, ok := d.reg.Get(registry.AgentSessionKey(claudecode.Agent, "s1"))
	if !ok || s.Cwd != "/w" {
		t.Fatalf("session = %+v, %v; want a claude session in /w", s, ok)
	}
}

// A rename the mod reports sets the name and leaves a pending prompt alone.
func TestModSessionRenameSetsTheName(t *testing.T) {
	d, srv := modTestNode(t)
	_, sid := askPermission(t, d, srv, "Bash", bashLs)
	modPost(t, context.Background(), srv.URL+"/claude/event", map[string]any{
		"event":   "SessionRename",
		"payload": map[string]any{"session_id": "s1", "cwd": "/w", "name": " New name "},
	}, nil)
	s, _ := d.reg.Get(sid)
	if s.Name != "New name" {
		t.Errorf("name = %q, want New name", s.Name)
	}
	if s.Interaction == nil || s.Status != session.StatusAwaitingInput {
		t.Errorf("status %q, interaction %+v; want the pending prompt kept", s.Status, s.Interaction)
	}
}

func TestModPermissionAnsweredInArgus(t *testing.T) {
	d, srv := modTestNode(t)
	id, sid := askPermission(t, d, srv, "Bash", bashLs)

	got := make(chan modDecisionResult, 1)
	go func() {
		var out modDecisionResult
		modPost(t, context.Background(), srv.URL+"/claude/decision", map[string]any{"requestId": id}, &out)
		got <- out
	}()
	params, _ := json.Marshal(map[string]any{"session_id": sid, "option_value": "allow"})
	if _, err := d.handleSessionRespond(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	out := <-got
	if !out.Done || !strings.Contains(out.Output, `"behavior":"allow"`) {
		t.Fatalf("decision = %+v, want done with an allow decision", out)
	}
}

func TestModAnswerBetweenWaitsIsKept(t *testing.T) {
	d, srv := modTestNode(t)
	id, sid := askPermission(t, d, srv, "Bash", bashLs)
	// The user answers while the mod is between /claude/decision requests.
	params, _ := json.Marshal(map[string]any{"session_id": sid, "option_value": "deny", "reason": "no"})
	if _, err := d.handleSessionRespond(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	var out modDecisionResult
	modPost(t, context.Background(), srv.URL+"/claude/decision", map[string]any{"requestId": id}, &out)
	if !out.Done || !strings.Contains(out.Output, `"behavior":"deny"`) {
		t.Fatalf("decision = %+v, want the deny given between waits", out)
	}
	// Handed over once: the request is finished now.
	var again modDecisionResult
	modPost(t, context.Background(), srv.URL+"/claude/decision", map[string]any{"requestId": id}, &again)
	if !again.Done || again.Output != "" {
		t.Fatalf("second decision = %+v, want done and empty", again)
	}
}

// An answer given late in the lease still reaches a mod that re-polls after
// the lease would have run out.
func TestModAnswerRenewsLease(t *testing.T) {
	old := modDecisionLease
	modDecisionLease = 300 * time.Millisecond
	t.Cleanup(func() { modDecisionLease = old })
	d, srv := modTestNode(t)
	id, sid := askPermission(t, d, srv, "Bash", bashLs)
	time.Sleep(200 * time.Millisecond)
	params, _ := json.Marshal(map[string]any{"session_id": sid, "option_value": "deny", "reason": "no"})
	if _, err := d.handleSessionRespond(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // past the first lease
	var out modDecisionResult
	modPost(t, context.Background(), srv.URL+"/claude/decision", map[string]any{"requestId": id}, &out)
	if !out.Done || !strings.Contains(out.Output, `"behavior":"deny"`) {
		t.Fatalf("decision = %+v, want the deny", out)
	}
}

func TestModDecisionPendingAfterHold(t *testing.T) {
	old := modHold
	modHold = 20 * time.Millisecond
	t.Cleanup(func() { modHold = old })
	d, srv := modTestNode(t)
	id, sid := askPermission(t, d, srv, "Bash", bashLs)

	var out modDecisionResult
	modPost(t, context.Background(), srv.URL+"/claude/decision", map[string]any{"requestId": id}, &out)
	if out.Done {
		t.Fatalf("decision = %+v, want not done after the hold", out)
	}
	if promptCleared(d, sid) {
		t.Fatal("the prompt must stay while the mod keeps waiting")
	}
}

func TestModPermissionLeaseClearsPrompt(t *testing.T) {
	old := modDecisionLease
	modDecisionLease = 200 * time.Millisecond
	t.Cleanup(func() { modDecisionLease = old })
	d, srv := modTestNode(t)
	id, sid := askPermission(t, d, srv, "Bash", bashLs)

	// Nobody waits (the user denied in Claude, so the hook is gone).
	waitFor(t, func() bool { return promptCleared(d, sid) })
	var out modDecisionResult
	modPost(t, context.Background(), srv.URL+"/claude/decision", map[string]any{"requestId": id}, &out)
	if !out.Done || out.Output != "" {
		t.Fatalf("decision after lease = %+v, want done and empty", out)
	}
}

// A local cancel ends the turn (Stop) before the lease ends the request: the
// unanswered request must leave the idle state as the Stop set it.
func TestModLeaseEndKeepsIdleAfterStop(t *testing.T) {
	old := modDecisionLease
	modDecisionLease = 200 * time.Millisecond
	t.Cleanup(func() { modDecisionLease = old })
	d, srv := modTestNode(t)
	_, sid := askPermission(t, d, srv, "Bash", bashLs)

	modPost(t, context.Background(), srv.URL+"/claude/event", map[string]any{
		"event":   "Stop",
		"payload": map[string]any{"session_id": "s1", "cwd": "/w", "stop_hook_active": false},
	}, nil)
	idle := func() bool {
		s, _ := d.reg.Get(sid)
		return s.Status == session.StatusAwaitingInput && s.Interaction != nil &&
			s.Interaction.Kind == session.InteractionIdle
	}
	if !idle() {
		s, _ := d.reg.Get(sid)
		t.Fatalf("after Stop: status=%q interaction=%+v, want awaiting_input idle", s.Status, s.Interaction)
	}
	waitFor(t, func() bool {
		d.pendingMu.Lock()
		defer d.pendingMu.Unlock()
		return d.pending[sid] == nil
	})
	if !idle() {
		s, _ := d.reg.Get(sid)
		t.Fatalf("after the lease: status=%q interaction=%+v, want awaiting_input idle", s.Status, s.Interaction)
	}
}

func TestModPermissionLeaseAfterClosedWait(t *testing.T) {
	old := modDecisionLease
	modDecisionLease = 200 * time.Millisecond
	t.Cleanup(func() { modDecisionLease = old })
	d, srv := modTestNode(t)
	id, sid := askPermission(t, d, srv, "Bash", bashLs)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		modPost(t, ctx, srv.URL+"/claude/decision", map[string]any{"requestId": id}, nil)
		close(done)
	}()
	waitFor(t, func() bool { return d.modWaitsOpen(id) == 1 })
	time.Sleep(300 * time.Millisecond) // longer than the lease: an open wait holds it off
	if promptCleared(d, sid) {
		t.Fatal("an open /claude/decision must hold the lease")
	}
	cancel() // the engine abandoned the hook mid-wait
	<-done
	waitFor(t, func() bool { return promptCleared(d, sid) })
}

func postToolEvent(t *testing.T, srv *httptest.Server, event, toolName string, toolInput map[string]any) {
	t.Helper()
	modPost(t, context.Background(), srv.URL+"/claude/event", map[string]any{
		"event": event,
		"payload": map[string]any{
			"session_id": "s1", "cwd": "/w", "tool_name": toolName,
			"tool_input": toolInput, "tool_use_id": "t1",
		},
	}, nil)
}

func parked(d *Node, sid string) bool {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	return d.pending[sid] != nil
}

func TestModPermissionEndsOnMatchingPostToolUse(t *testing.T) {
	d, srv := modTestNode(t)
	id, sid := askPermission(t, d, srv, "Bash", bashLs)

	postToolEvent(t, srv, "PostToolUse", "Bash", map[string]any{"command": "pwd"}) // another call: the request stays
	if !modRequestOpen(d, id) {
		t.Fatal("a PostToolUse for a different call must not end the request")
	}

	// The user allowed it in Claude and it ran; the recorded input may carry more fields.
	postToolEvent(t, srv, "PostToolUse", "Bash", map[string]any{"command": "ls", "description": "list"})
	waitFor(t, func() bool { return !parked(d, sid) })
	var out modDecisionResult
	modPost(t, context.Background(), srv.URL+"/claude/decision", map[string]any{"requestId": id}, &out)
	if !out.Done || out.Output != "" {
		t.Fatalf("decision = %+v, want done and empty", out)
	}
}

func modRequestOpen(d *Node, id string) bool {
	d.modPermsMu.Lock()
	defer d.modPermsMu.Unlock()
	return d.modPerms[id] != nil
}

func TestModPermissionEndsOnMatchingPostToolUseFailure(t *testing.T) {
	d, srv := modTestNode(t)
	id, sid := askPermission(t, d, srv, "Bash", bashLs)

	postToolEvent(t, srv, "PostToolUseFailure", "Bash", map[string]any{"command": "pwd"})
	if !modRequestOpen(d, id) {
		t.Fatal("a failure for a different call must not end the request")
	}
	postToolEvent(t, srv, "PostToolUseFailure", "Bash", map[string]any{"command": "ls"})
	waitFor(t, func() bool { return !parked(d, sid) })
	if modRequestOpen(d, id) {
		t.Fatal("the request must be forgotten")
	}
}

// Answering a question or approving a plan in Claude changes the tool input the
// PostToolUse reports (answers added; the plan dropped). Verified live on 2.1.296.
func TestModPermissionEndsOnInteractivePostToolUse(t *testing.T) {
	questions := []any{map[string]any{"question": "Tea or coffee?", "options": []any{"Tea", "Coffee"}}}
	for _, tc := range []struct {
		tool       string
		asked, ran map[string]any
	}{
		{"AskUserQuestion", map[string]any{"questions": questions},
			map[string]any{"questions": questions, "answers": map[string]any{"Tea or coffee?": "Tea"}, "annotations": map[string]any{}}},
		{"ExitPlanMode", map[string]any{"plan": "write plan.txt"}, map[string]any{}},
	} {
		d, srv := modTestNode(t)
		_, sid := askPermission(t, d, srv, tc.tool, tc.asked)
		postToolEvent(t, srv, "PostToolUse", tc.tool, tc.ran)
		waitFor(t, func() bool { return !parked(d, sid) })
	}
}

func TestModPermissionCancel(t *testing.T) {
	d, srv := modTestNode(t)
	id, sid := askPermission(t, d, srv, "Bash", bashLs)
	modPost(t, context.Background(), srv.URL+"/claude/cancel", map[string]any{"requestId": id}, nil)
	waitFor(t, func() bool { return promptCleared(d, sid) })
}

func TestModDecisionUnknownRequestIsDone(t *testing.T) {
	_, srv := modTestNode(t)
	var out modDecisionResult
	modPost(t, context.Background(), srv.URL+"/claude/decision", map[string]any{"requestId": "nope"}, &out)
	if !out.Done || out.Output != "" {
		t.Fatalf("decision = %+v, want done and empty", out)
	}
}

func TestModPollReturnsSentPrompt(t *testing.T) {
	d, srv := modTestNode(t)
	type result struct {
		Commands []modbridge.Command `json:"commands"`
	}
	got := make(chan result, 1)
	go func() {
		var r result
		modPost(t, context.Background(), srv.URL+"/claude/poll", map[string]any{"sessionId": "s1"}, &r)
		for _, c := range r.Commands {
			modPost(t, context.Background(), srv.URL+"/claude/ack", map[string]any{"id": c.ID}, nil)
		}
		got <- r
	}()
	bridge := d.adapterFor(claudecode.Agent).(adapter.ModBridged).ModBridge()
	waitFor(t, func() bool { return bridge.Reachable("s1") })

	p := d.adapterFor(claudecode.Agent).(adapter.Prompter)
	if err := p.SendPrompt(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "hi"); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if len(r.Commands) != 1 || r.Commands[0].Kind != modbridge.KindPrompt || r.Commands[0].Text != "hi" {
		t.Fatalf("commands = %v, want one prompt", r.Commands)
	}
}

func TestModPollAnswersEmptyAfterHold(t *testing.T) {
	old := modHold
	modHold = 20 * time.Millisecond
	t.Cleanup(func() { modHold = old })
	_, srv := modTestNode(t)

	var raw map[string]json.RawMessage
	modPost(t, context.Background(), srv.URL+"/claude/poll", map[string]any{"sessionId": "s1"}, &raw)
	if string(raw["commands"]) != "[]" {
		t.Fatalf("commands = %s, want []", raw["commands"])
	}
}

func TestModRequestsNeedTheirIDs(t *testing.T) {
	_, srv := modTestNode(t)
	for _, path := range []string{"/claude/poll", "/claude/decision", "/claude/cancel"} {
		if code := modPost(t, context.Background(), srv.URL+path, map[string]any{}, nil); code != http.StatusBadRequest {
			t.Errorf("%s with no id: status %d, want 400", path, code)
		}
	}
}

func TestModEventRejectsOversizedBody(t *testing.T) {
	_, srv := modTestNode(t)
	body := `{"event":"Stop","payload":"` + strings.Repeat("x", 9<<20) + `"}`
	res, err := http.Post(srv.URL+"/claude/event", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode < 400 || res.StatusCode >= 500 {
		t.Fatalf("status = %d, want 4xx for a body over 8 MiB", res.StatusCode)
	}
}

func TestRunServesModSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	socket := shortSocket(t)
	go func() { _ = d.Run(ctx, socket) }()

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", ModSocketPath(socket))
		},
	}}
	var res *http.Response
	waitFor(t, func() bool {
		var err error
		res, err = client.Post("http://argus/claude/poll", "application/json", strings.NewReader(`{}`))
		return err == nil
	})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 from the mod socket", res.StatusCode)
	}
	for _, path := range []string{socket, ModSocketPath(socket)} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, perm)
		}
	}
}

func TestModSocketPathIsNamedAfterSocket(t *testing.T) {
	for in, want := range map[string]string{
		"/run/user/1000/argus/argus.sock": "/run/user/1000/argus/argus.mod.sock",
		"/tmp/argus/d.sock":               "/tmp/argus/d.mod.sock",
		"/tmp/argus/node":                 "/tmp/argus/node.mod.sock",
	} {
		if got := ModSocketPath(in); got != want {
			t.Errorf("ModSocketPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunRefusesTooLongModSocket(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("x", maxSocketPath))
	err := newNode(map[session.TmuxServer]*tmux.Client{}).Run(context.Background(), filepath.Join(dir, "d.sock"))
	if err == nil || !strings.Contains(err.Error(), "unix socket limit") {
		t.Fatalf("err = %v, want the socket length error", err)
	}
	if _, serr := os.Stat(dir); !os.IsNotExist(serr) {
		t.Fatalf("Run created %s before refusing", dir)
	}
}

func TestModAckCompletesSend(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	b := d.adapterFor(claudecode.Agent).(adapter.ModBridged).ModBridge()
	cmds := make(chan []modbridge.Command, 1)
	go func() { cmds <- b.Wait(context.Background(), "s1", time.Minute) }()
	waitFor(t, func() bool { return b.Reachable("s1") })
	sent := make(chan error, 1)
	go func() {
		_, err := b.Send(context.Background(), "s1", modbridge.Command{Kind: modbridge.KindPrompt, Text: "/nope"})
		sent <- err
	}()
	c := (<-cmds)[0]

	rec := httptest.NewRecorder()
	d.modHandler().ServeHTTP(rec, httptest.NewRequest("POST", "/claude/ack", strings.NewReader(`{"id":"`+c.ID+`","error":"unknown command"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if err := <-sent; err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("Send err = %v, want the mod's failure", err)
	}

	rec = httptest.NewRecorder()
	d.modHandler().ServeHTTP(rec, httptest.NewRequest("POST", "/claude/ack", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d for an ack without id, want 400", rec.Code)
	}
}

// An ack says whether the command ran or was only accepted to run later.
func TestModAckReportsWhetherTheCommandRan(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	b := d.adapterFor(claudecode.Agent).(adapter.ModBridged).ModBridge()
	for _, tc := range []struct {
		body func(id string) string
		ran  bool
	}{
		{func(id string) string { return `{"id":"` + id + `","ran":true}` }, true},
		{func(id string) string { return `{"id":"` + id + `"}` }, false},
	} {
		cmds := make(chan []modbridge.Command, 1)
		go func() { cmds <- b.Wait(context.Background(), "s1", time.Minute) }()
		waitFor(t, func() bool { return b.Reachable("s1") })
		type result struct {
			ran bool
			err error
		}
		sent := make(chan result, 1)
		go func() {
			ran, err := b.Send(context.Background(), "s1", modbridge.Command{Kind: modbridge.KindPrompt, Text: "/rename x"})
			sent <- result{ran, err}
		}()
		c := (<-cmds)[0]
		d.modHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/claude/ack", strings.NewReader(tc.body(c.ID))))
		if r := <-sent; r.err != nil || r.ran != tc.ran {
			t.Errorf("Send = (%v, %v) for %s, want (%v, nil)", r.ran, r.err, tc.body(c.ID), tc.ran)
		}
	}
}

func TestModRoutesByAgent(t *testing.T) {
	_, srv := modTestNode(t)
	for path, want := range map[string]int{
		"/nope/poll":     http.StatusNotFound,   // unknown agent
		"/opencode/poll": http.StatusNotFound,   // an agent with no mod bridge
		"/claude/ack":    http.StatusBadRequest, // reaches the handler
	} {
		if got := modPost(t, context.Background(), srv.URL+path, map[string]any{"sessionId": "s1"}, nil); got != want {
			t.Errorf("%s: status = %d, want %d", path, got, want)
		}
	}
}

// An answer taken by a wait whose mod hung up stays for the mod's next wait.
func TestModAnswerSurvivesHungUpWait(t *testing.T) {
	d, _ := modTestNode(t)
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	// The hung-up wait sees the answer and its done ctx at once; run it enough
	// times that select takes the answer.
	for range 20 {
		p := &modPermission{id: "r1", out: make(chan string, 1), cancel: func() {}, lease: time.NewTimer(time.Hour)}
		p.out <- "answer"
		d.modPermsMu.Lock()
		d.modPerms[p.id] = p
		d.modPermsMu.Unlock()
		if _, done := d.waitModPermission(gone, p.id); done {
			t.Fatal("a hung-up wait must not take the answer")
		}
		if out, done := d.waitModPermission(context.Background(), p.id); !done || out != "answer" {
			t.Fatalf("next wait = %q, %v; want the answer", out, done)
		}
		p.lease.Stop()
	}
}

func (d *Node) modWaitsOpen(id string) int {
	d.modPermsMu.Lock()
	defer d.modPermsMu.Unlock()
	if p := d.modPerms[id]; p != nil {
		return p.open
	}
	return 0
}
