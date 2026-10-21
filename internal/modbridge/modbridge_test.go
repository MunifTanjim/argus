package modbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testGrace      = 100 * time.Millisecond
	testAckTimeout = 200 * time.Millisecond
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 1s")
		}
		time.Sleep(time.Millisecond)
	}
}

// openPoll starts a Wait and returns a channel with its result. Like the mod,
// it acks every command it receives.
func openPoll(b *Bridge, ctx context.Context, id string, hold time.Duration) <-chan []Command {
	out := make(chan []Command, 1)
	go func() {
		cmds := b.Wait(ctx, id, hold, nil)
		for _, c := range cmds {
			b.Ack(c.ID, "", true, nil)
		}
		out <- cmds
	}()
	return out
}

func sameCommand(a, b Command) bool { return a.Kind == b.Kind && a.Text == b.Text }

func TestSendWithoutPollIsUnreachable(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	_, err := b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "hi"})
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestSendReachesOpenPoll(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	got := openPoll(b, context.Background(), "s1", time.Minute)
	waitFor(t, func() bool { return b.Reachable("s1") })

	want := Command{Kind: KindPrompt, Text: "hi"}
	if _, err := b.Send(context.Background(), "s1", want); err != nil {
		t.Fatal(err)
	}
	if cmds := <-got; len(cmds) != 1 || !sameCommand(cmds[0], want) {
		t.Fatalf("poll got %v, want [%v]", cmds, want)
	}
}

func TestCommandIsDeliveredAtMostOnce(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	first := openPoll(b, context.Background(), "s1", time.Minute)
	waitFor(t, func() bool { return b.Reachable("s1") })
	if _, err := b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "once"}); err != nil {
		t.Fatal(err)
	}
	<-first
	// The next poll must not see the command again.
	if cmds := b.Wait(context.Background(), "s1", 20*time.Millisecond, nil); len(cmds) != 0 {
		t.Fatalf("second poll got %v, want none", cmds)
	}
}

func TestSendFailsWithModError(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	go func() {
		for _, c := range b.Wait(context.Background(), "s1", time.Minute, nil) {
			b.Ack(c.ID, "unknown command", false, nil)
		}
	}()
	waitFor(t, func() bool { return b.Reachable("s1") })
	_, err := b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "/nope"})
	if err == nil || err.Error() != "claude: unknown command" {
		t.Fatalf("err = %v, want the mod's failure", err)
	}
}

// A poll whose client went away takes its command with it; Send must not report success.
func TestSendFailsWithoutAck(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	got := make(chan []Command, 1)
	go func() { got <- b.Wait(context.Background(), "s1", time.Minute, nil) }()
	waitFor(t, func() bool { return b.Reachable("s1") })
	_, err := b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "hi"})
	if !errors.Is(err, ErrNoAck) {
		t.Fatalf("err = %v, want ErrNoAck", err)
	}
	if cmds := <-got; len(cmds) != 1 {
		t.Fatalf("poll got %v, want one command", cmds)
	}
}

func TestLateAckIsIgnored(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	got := make(chan []Command, 1)
	go func() { got <- b.Wait(context.Background(), "s1", time.Minute, nil) }()
	waitFor(t, func() bool { return b.Reachable("s1") })
	_, _ = b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "hi"})
	b.Ack((<-got)[0].ID, "", true, nil)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.acks) != 0 {
		t.Fatalf("acks = %v, want none left", b.acks)
	}
}

// A Send waiting for a re-poll stops when the session expires, and leaves nothing behind.
func TestExpiryReleasesWaitingSend(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	b.Wait(context.Background(), "s1", time.Millisecond, nil)
	if _, err := b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "hi"}); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.conns) == 0
	})
}

func TestWaitAnswersEmptyAfterHold(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	if cmds := b.Wait(context.Background(), "s1", 10*time.Millisecond, nil); cmds != nil {
		t.Fatalf("got %v, want nil", cmds)
	}
}

func TestWaitEndsWithContext(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	ctx, cancel := context.WithCancel(context.Background())
	got := openPoll(b, ctx, "s1", time.Minute)
	waitFor(t, func() bool { return b.Reachable("s1") })
	cancel()
	select {
	case cmds := <-got:
		if cmds != nil {
			t.Fatalf("got %v, want nil", cmds)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after cancel")
	}
}

func TestNewerPollSupersedesOlder(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	ctx, cancel := context.WithCancel(context.Background())
	older := openPoll(b, ctx, "s1", time.Minute)
	waitFor(t, func() bool { return b.Reachable("s1") })
	newer := openPoll(b, context.Background(), "s1", time.Minute)
	time.Sleep(10 * time.Millisecond)

	want := Command{Kind: KindPrompt, Text: "/compact"}
	if _, err := b.Send(context.Background(), "s1", want); err != nil {
		t.Fatal(err)
	}
	if cmds := <-newer; len(cmds) != 1 || !sameCommand(cmds[0], want) {
		t.Fatalf("newer poll got %v, want [%v]", cmds, want)
	}
	select {
	case cmds := <-older:
		t.Fatalf("older poll returned %v before its hold or cancel", cmds)
	default:
	}
	cancel()
	select {
	case cmds := <-older:
		if cmds != nil {
			t.Fatalf("older poll got %v, want nil", cmds)
		}
	case <-time.After(time.Second):
		t.Fatal("older poll did not end with its context")
	}
}

// Two Claude processes sharing a session id must not make each other re-poll at once.
func TestSupersededPollWaitsOutItsHold(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	const hold = 150 * time.Millisecond
	start := time.Now()
	older := openPoll(b, context.Background(), "s1", hold)
	waitFor(t, func() bool { return b.Reachable("s1") })
	newer := openPoll(b, context.Background(), "s1", time.Minute)

	select {
	case cmds := <-older:
		if cmds != nil {
			t.Fatalf("older poll got %v, want nil", cmds)
		}
		if waited := time.Since(start); waited < hold {
			t.Fatalf("superseded poll returned after %v, before its %v hold", waited, hold)
		}
	case <-time.After(time.Second):
		t.Fatal("superseded poll did not end at its hold")
	}
	// The newer poll still owns the session.
	if !b.Reachable("s1") {
		t.Fatal("session unreachable while the newer poll is open")
	}
	if _, err := b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if cmds := <-newer; len(cmds) != 1 {
		t.Fatalf("newer poll got %v, want one command", cmds)
	}
}

func TestSendWaitsForRepollWithinGrace(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	b.Wait(context.Background(), "s1", time.Millisecond, nil) // closes; grace starts

	sent := make(chan error, 1)
	go func() {
		_, err := b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "hi"})
		sent <- err
	}()
	time.Sleep(10 * time.Millisecond)
	got := openPoll(b, context.Background(), "s1", time.Minute)

	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if cmds := <-got; len(cmds) != 1 {
		t.Fatalf("re-poll got %v, want one command", cmds)
	}
}

func TestSendFailsWhenGraceRunsOut(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	b.Wait(context.Background(), "s1", time.Millisecond, nil)
	start := time.Now()
	_, err := b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "hi"})
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if time.Since(start) > testGrace+50*time.Millisecond {
		t.Fatalf("Send waited %v, longer than grace", time.Since(start))
	}
}

func TestReachabilityChangesAreReported(t *testing.T) {
	var mu sync.Mutex
	var changes []bool
	var b *Bridge
	b = New(testGrace, testAckTimeout, func(id string) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, b.Reachable(id))
	})
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(changes)
	}
	b.Wait(context.Background(), "s1", time.Millisecond, nil)
	waitFor(t, func() bool { return count() == 2 })
	// argusd back after an outage: the next poll makes the session reachable again.
	b.Wait(context.Background(), "s1", time.Millisecond, nil)
	waitFor(t, func() bool { return count() == 4 })
	mu.Lock()
	defer mu.Unlock()
	if want := []bool{true, false, true, false}; !equal(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
}

func equal(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAckBusyFailsTheSendWithErrBusy(t *testing.T) {
	b := New(time.Second, time.Second, nil)
	ctx := context.Background()
	go func() {
		for _, c := range b.Wait(ctx, "s1", time.Minute, nil) {
			b.Ack(c.ID, FailBusy, false, nil)
		}
	}()
	for !b.Reachable("s1") {
		time.Sleep(time.Millisecond)
	}
	_, err := b.Send(ctx, "s1", Command{Kind: KindFill, Text: "steer"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestDeliverRefusesKindsThePollDidNotList(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Wait(ctx, "s1", time.Minute, []string{KindPrompt, KindCommands}); close(done) }()
	waitFor(t, func() bool { return b.Reachable("s1") })
	if _, err := b.Send(context.Background(), "s1", Command{Kind: KindFill}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("fill err = %v, want ErrUnsupported", err)
	}
	cancel()
	<-done
}

func TestPollWithoutKindsRunsLegacyKinds(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	go func() {
		for _, c := range b.Wait(context.Background(), "s1", time.Minute, nil) {
			b.Ack(c.ID, "", true, nil)
		}
	}()
	waitFor(t, func() bool { return b.Reachable("s1") })
	if _, err := b.Request(context.Background(), "s1", Command{Kind: KindCommands}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("commands err = %v, want ErrUnsupported", err)
	}
	if ran, err := b.Send(context.Background(), "s1", Command{Kind: KindPrompt, Text: "hi"}); err != nil || !ran {
		t.Fatalf("prompt = (%v, %v), want (true, nil)", ran, err)
	}
}

func TestRequestReturnsAckData(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	want := `[{"name":"x"}]`
	go func() {
		for _, c := range b.Wait(context.Background(), "s1", time.Minute, []string{KindCommands}) {
			b.Ack(c.ID, "", true, json.RawMessage(want))
		}
	}()
	waitFor(t, func() bool { return b.Reachable("s1") })
	got, err := b.Request(context.Background(), "s1", Command{Kind: KindCommands})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("data = %s, want %s", got, want)
	}
}

func TestRequestPropagatesFailure(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	go func() {
		for _, c := range b.Wait(context.Background(), "s1", time.Minute, []string{KindCommands}) {
			b.Ack(c.ID, "boom", false, nil)
		}
	}()
	waitFor(t, func() bool { return b.Reachable("s1") })
	_, err := b.Request(context.Background(), "s1", Command{Kind: KindCommands})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the mod's failure", err)
	}
}

// A poll that lapsed into grace after listing commands must not hand the kind
// to a re-poll that did not list it.
func TestRequestRefusedByRepollWithoutKind(t *testing.T) {
	b := New(testGrace, testAckTimeout, nil)
	b.Wait(context.Background(), "s1", time.Millisecond, []string{KindCommands})

	type result struct {
		data json.RawMessage
		err  error
	}
	got := make(chan result, 1)
	go func() {
		data, err := b.Request(context.Background(), "s1", Command{Kind: KindCommands})
		got <- result{data, err}
	}()
	time.Sleep(10 * time.Millisecond)
	polled := make(chan []Command, 1)
	go func() { polled <- b.Wait(context.Background(), "s1", 50*time.Millisecond, nil) }()

	r := <-got
	if !errors.Is(r.err, ErrUnsupported) {
		t.Fatalf("Request = (%s, %v), want ErrUnsupported", r.data, r.err)
	}
	if cmds := <-polled; len(cmds) != 0 {
		t.Fatalf("nil-kinds poll got %v, want none", cmds)
	}
}
