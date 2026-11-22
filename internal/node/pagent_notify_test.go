package node

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/session"
)

type recordSink struct {
	mu  sync.Mutex
	got []push.Notification
}

func (s *recordSink) Notify(_ context.Context, n push.Notification) {
	s.mu.Lock()
	s.got = append(s.got, n)
	s.mu.Unlock()
}

func (s *recordSink) all() []push.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]push.Notification(nil), s.got...)
}

// notifyRig is a node with one agent, a chat thread, a recording push sink
// and a settable clock.
type notifyRig struct {
	d    *Node
	s    *pagent.Store
	a    pagent.Agent
	chat string
	sink *recordSink

	mu    sync.Mutex
	clock time.Time
}

func newNotifyRig(t *testing.T) *notifyRig {
	t.Helper()
	d, s, _ := pagentNode(t)
	r := &notifyRig{d: d, s: s, chat: "th_chat", sink: &recordSink{}, clock: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	d.pagentNow = r.now
	d.pagentCfg.Defaults.Timezone = "UTC"
	d.pagentCfg.Defaults.Notify = "needs-you"
	sinks := []push.Sink{r.sink}
	d.ownerSinks.Store(&sinks)
	info, err := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	r.a, _, _ = s.AgentByID(context.Background(), info.ID)
	if err := s.InsertThread(context.Background(), pagent.Thread{ID: r.chat, AgentID: r.a.ID, Kind: threadChat, StartedBy: "you", Status: pagent.ThreadActive}); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *notifyRig) now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clock
}

func (r *notifyRig) set(t time.Time) {
	r.mu.Lock()
	r.clock = t
	r.mu.Unlock()
}

// pushes waits briefly for n pushes and returns what arrived.
func (r *notifyRig) pushes(t *testing.T, n int) []push.Notification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(r.sink.all()) < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	return r.sink.all()
}

func TestNotifyPolicyTable(t *testing.T) {
	for _, tc := range []struct {
		notify, kind string
		want         bool
	}{
		{"all", NotifyNeedsYou, true}, {"all", NotifyResult, true}, {"all", NotifyBrief, true}, {"all", NotifyOffer, true}, {"all", NotifyQuiet, false},
		{"needs-you", NotifyNeedsYou, true}, {"needs-you", NotifyResult, false}, {"needs-you", NotifyBrief, false}, {"needs-you", NotifyOffer, false}, {"needs-you", NotifyQuiet, false},
		{"off", NotifyNeedsYou, false}, {"off", NotifyResult, false}, {"off", NotifyBrief, false}, {"off", NotifyOffer, false}, {"off", NotifyQuiet, false},
		{"", NotifyNeedsYou, true}, {"", NotifyResult, false},
	} {
		if got := shouldPush(tc.notify, tc.kind); got != tc.want {
			t.Errorf("notify %q kind %q: %v, want %v", tc.notify, tc.kind, got, tc.want)
		}
	}
	for result, want := range map[string]string{"needs_you": NotifyNeedsYou, "blocked": NotifyNeedsYou, "progress": NotifyResult, "done": NotifyResult, "no_change": NotifyQuiet} {
		if got := resultNotifyKind(result); got != want {
			t.Errorf("result %s: %s, want %s", result, got, want)
		}
	}

	r := newNotifyRig(t)
	ctx := context.Background()
	r.d.SetIdentity("home", "")
	r.d.pagentNotify(ctx, r.a, r.chat, NotifyResult, "routine", "scout: inbox progress", "two mails")
	r.d.pagentNotify(ctx, r.a, r.chat, NotifyNeedsYou, "routine", "scout: inbox needs you", "log in please")
	got := r.pushes(t, 1)
	if len(got) != 1 || got[0].Title != "scout: inbox needs you" || got[0].Data["pagent_id"] != r.a.ID || got[0].Data["pagent_thread_id"] != r.chat || got[0].Data["node_id"] != "home" {
		t.Fatalf("pushes: %+v", got)
	}
	r.d.pagentCfg.Defaults.Notify = "all"
	r.d.pagentNotify(ctx, r.a, r.chat, NotifyResult, "routine", "scout: inbox progress", "two mails")
	r.d.pagentNotify(ctx, r.a, r.chat, NotifyQuiet, "routine", "memory", "")
	if got := r.pushes(t, 2); len(got) != 2 {
		t.Fatalf("notify all: %+v", got)
	}
}

func TestNotifyQuietHoursHoldAndSummary(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()
	r.d.pagentCfg.Defaults.QuietHours = "22:00-07:00"
	r.set(time.Date(2026, 11, 22, 23, 0, 0, 0, time.UTC))
	r.d.pagentNotify(ctx, r.a, r.chat, NotifyNeedsYou, "routine", "scout: inbox blocked", "site down")
	r.d.pagentNotify(ctx, r.a, r.chat, NotifyNeedsYou, "check-back", "scout: pr needs you", "review")
	// Work the user started still pushes.
	r.d.pagentNotify(ctx, r.a, r.chat, NotifyNeedsYou, "you", "scout needs you", "captcha")
	if got := r.pushes(t, 1); len(got) != 1 || got[0].Title != "scout needs you" {
		t.Fatalf("during quiet hours: %+v", got)
	}
	r.set(time.Date(2026, 11, 23, 6, 59, 0, 0, time.UTC))
	r.d.flushQuietHolds(ctx, r.now())
	if got := r.pushes(t, 1); len(got) != 1 {
		t.Fatalf("flushed before the end: %+v", got)
	}
	r.set(time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC))
	r.d.flushQuietHolds(ctx, r.now())
	got := r.pushes(t, 2)
	if len(got) != 2 || got[1].Title != "scout: 2 updates while quiet" || !strings.Contains(got[1].Body, "site down") || !strings.Contains(got[1].Body, "review") {
		t.Fatalf("summary: %+v", got)
	}
	r.d.flushQuietHolds(ctx, r.now())
	if got := r.pushes(t, 3); len(got) != 2 {
		t.Fatalf("summary sent twice: %+v", got)
	}
}

func TestNotifyPresenceDelayCancelledByRead(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()
	r.d.pagentCfg.Defaults.NotifyDelay = 50 * time.Millisecond
	n := &fakeNotifier{ch: make(chan api.Notification, 64)}
	r.d.registerConn(n)
	if _, err := r.d.handleSessionsList(api.WithNotifier(ctx, n), nil); err != nil {
		t.Fatal(err)
	}
	r.set(r.now().Add(time.Minute)) // the client has been attached a while

	r.d.pagentNotify(ctx, r.a, r.chat, NotifyNeedsYou, "routine", "first", "")
	if got := r.sink.all(); len(got) != 0 {
		t.Fatalf("pushed at once while a client is attached: %+v", got)
	}
	r.set(r.now().Add(time.Second))
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentThreadMarkRead, api.PAgentMarkReadParams{ThreadID: r.chat}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := r.sink.all(); len(got) != 0 {
		t.Fatalf("pushed after the thread was read: %+v", got)
	}

	r.set(r.now().Add(time.Second))
	r.d.pagentNotify(ctx, r.a, r.chat, NotifyNeedsYou, "routine", "second", "")
	if got := r.pushes(t, 1); len(got) != 1 || got[0].Title != "second" {
		t.Fatalf("unread push after the delay: %+v", got)
	}
}

func TestNotifyHealthDedupe(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()
	r.d.pagentHealth(ctx, r.a, r.chat, "cli-missing", "scout: claude not found", "")
	r.d.pagentHealth(ctx, r.a, r.chat, "cli-missing", "scout: claude not found", "")
	if got := r.pushes(t, 2); len(got) != 1 {
		t.Fatalf("deduped: %+v", got)
	}
	r.set(r.now().Add(25 * time.Hour))
	r.d.pagentHealth(ctx, r.a, r.chat, "cli-missing", "scout: claude not found", "")
	if got := r.pushes(t, 2); len(got) != 2 {
		t.Fatalf("re-push after 24h: %+v", got)
	}
	r.d.pagentHealthResolved(r.a, "cli-missing")
	r.d.pagentHealth(ctx, r.a, r.chat, "cli-missing", "scout: claude not found", "")
	if got := r.pushes(t, 3); len(got) != 3 {
		t.Fatalf("after resolve: %+v", got)
	}
}

func TestRunResultPushes(t *testing.T) {
	r := newRunRig(t)
	sink := &recordSink{}
	sinks := []push.Sink{sink}
	r.d.ownerSinks.Store(&sinks)
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	for _, res := range []string{"progress", "no_change", "needs_you"} {
		c := r.start(t, rt)
		r.report(t, c, reportRunParams{Result: res, Summary: "s-" + res})
		r.wait(t)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(sink.all()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	got := sink.all()
	if len(got) != 1 || got[0].Body != "s-needs_you" || got[0].Data["pagent_thread_id"] == "" {
		t.Fatalf("pushes: %+v", got)
	}
}

func TestHandoffPushesOnce(t *testing.T) {
	r := newMemRig(t, 0)
	sink := &recordSink{}
	sinks := []push.Sink{sink}
	r.d.ownerSinks.Store(&sinks)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, stop := r.d.reg.Subscribe()
	defer stop()
	done := make(chan struct{})
	go func() { push.Watch(ctx, events, push.Sinks{Immediate: []push.Sink{sink}}, nil); close(done) }()

	hookSession(r.d, "%1", "c1")
	s, _ := r.d.reg.FindByAgentSession("claude", "c1")
	caller := callerRef{Local: &session.Session{ID: s.ID, PAgentID: r.a.ID, PAgentThreadID: r.th}}
	if _, err := r.d.mcpRequestHandoff(ctx, caller, requestHandoffParams{Reason: "log in to the bank", Steps: "sign in"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(sink.all()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	got := sink.all()
	if len(got) != 1 || got[0].Data["pagent_thread_id"] != r.th || got[0].Body != "log in to the bank" {
		t.Fatalf("pushes: %+v", got)
	}
	cancel()
	<-done
}
