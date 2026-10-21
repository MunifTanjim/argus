package tui

import (
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

type swapFakeClient struct {
	events      chan api.Notification
	states      chan bool
	quarantined bool

	mu     sync.Mutex
	calls  []string
	closed bool
}

func newSwapFakeClient(quarantined bool) *swapFakeClient {
	return &swapFakeClient{
		events:      make(chan api.Notification, 4),
		states:      make(chan bool, 4),
		quarantined: quarantined,
	}
}

func (c *swapFakeClient) Call(method string, _, _ any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, method)
	return nil
}
func (c *swapFakeClient) Events() <-chan api.Notification { return c.events }
func (c *swapFakeClient) States() <-chan bool             { return c.states }
func (c *swapFakeClient) Reconnect()                      {}
func (c *swapFakeClient) Quarantined() bool               { return c.quarantined }
func (c *swapFakeClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *swapFakeClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *swapFakeClient) callLog() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func TestSwapClientForwardsTheNewClientAfterASwap(t *testing.T) {
	old, next := newSwapFakeClient(false), newSwapFakeClient(false)
	s := newSwapClient(old)
	defer s.Close()

	old.events <- api.Notification{Method: "before"}
	select {
	case ev := <-s.Events():
		if ev.Method != "before" {
			t.Fatalf("got %q, want before", ev.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("old client's event was not forwarded before the swap")
	}

	s.swap(next)
	old.events <- api.Notification{Method: "old"}
	next.events <- api.Notification{Method: "new"}

	select {
	case ev := <-s.Events():
		if ev.Method != "new" {
			t.Fatalf("got %q, want new", ev.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("new client's event was not forwarded")
	}
	select {
	case ev := <-s.Events():
		t.Fatalf("unexpected event %q after the swap", ev.Method)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSwapClientClosesTheOldClientAndReportsConnected(t *testing.T) {
	old, next := newSwapFakeClient(false), newSwapFakeClient(false)
	s := newSwapClient(old)
	defer s.Close()

	s.swap(next)
	if !old.isClosed() {
		t.Fatal("old client was not closed")
	}
	select {
	case connected := <-s.States():
		if !connected {
			t.Fatal("got connected=false after the swap, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("no state after the swap")
	}
}

func TestSwapClientCallsAndQuarantineUseTheCurrentClient(t *testing.T) {
	old, next := newSwapFakeClient(true), newSwapFakeClient(false)
	s := newSwapClient(old)
	defer s.Close()

	if !s.Quarantined() {
		t.Fatal("Quarantined() = false before the swap, want true")
	}
	s.swap(next)
	if s.Quarantined() {
		t.Fatal("Quarantined() = true after the swap, want false")
	}
	if err := s.Call("ping", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := next.callLog(); len(got) != 1 || got[0] != "ping" {
		t.Fatalf("new client calls = %v, want [ping]", got)
	}
	if got := old.callLog(); len(got) != 0 {
		t.Fatalf("old client calls = %v, want none", got)
	}
}

func TestSwapClientConcurrentCallsDuringSwap(t *testing.T) {
	s := newSwapClient(newSwapFakeClient(false))
	defer s.Close()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_ = s.Call("ping", nil, nil)
				_ = s.Quarantined()
			}
		}()
	}
	go func() {
		for range s.States() {
		}
	}()
	for range 5 {
		s.swap(newSwapFakeClient(false))
	}
	wg.Wait()
}

func TestSwapClientEndsForwardingWhenTheInnerEventsClose(t *testing.T) {
	c := newSwapFakeClient(false)
	s := newSwapClient(c)
	close(c.events)
	select {
	case ev, ok := <-s.Events():
		t.Fatalf("Events() yielded %v (ok=%v) after the inner events closed", ev, ok)
	case <-time.After(50 * time.Millisecond):
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-s.Events(); ok {
		t.Fatal("Events() still open after Close")
	}
}

func TestSwapClientLatestInnerStateWinsOverThePendingConnected(t *testing.T) {
	old, next := newSwapFakeClient(false), newSwapFakeClient(false)
	s := newSwapClient(old)
	defer s.Close()

	s.swap(next)
	next.states <- false
	deadline := time.Now().Add(time.Second)
	for len(next.states) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(next.states) > 0 {
		t.Fatal("the forwarder did not take the inner state")
	}

	select {
	case st := <-s.States():
		if st {
			t.Fatal("got the post-swap true, want the newer inner false")
		}
	case <-time.After(time.Second):
		t.Fatal("no state after the swap")
	}
	select {
	case st := <-s.States():
		t.Fatalf("got a second state %v, want one", st)
	case <-time.After(50 * time.Millisecond):
	}
}
