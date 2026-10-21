package tui

import (
	"sync"

	"github.com/MunifTanjim/argus/internal/api"
)

// swapClient lets the TUI replace its connection while it runs. Events and
// States stay the same channels across swaps; only one forwarder goroutine
// writes to them at a time.
type swapClient struct {
	events chan api.Notification
	states chan bool

	mu     sync.RWMutex
	cur    Client
	stop   chan struct{}
	done   chan struct{}
	closed bool
}

func newSwapClient(c Client) *swapClient {
	s := &swapClient{
		events: make(chan api.Notification),
		states: make(chan bool),
		cur:    c,
	}
	s.forward(c, false)
	return s
}

func (s *swapClient) current() Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *swapClient) Call(method string, params, out any) error {
	return s.current().Call(method, params, out)
}

func (s *swapClient) Events() <-chan api.Notification { return s.events }
func (s *swapClient) States() <-chan bool             { return s.states }
func (s *swapClient) Reconnect()                      { s.current().Reconnect() }
func (s *swapClient) Quarantined() bool               { return s.current().Quarantined() }

func (s *swapClient) swap(next Client) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = next.Close()
		return
	}
	old := s.cur
	s.stopForwarding()
	s.cur = next
	s.forward(next, true)
	s.mu.Unlock()
	_ = old.Close()
}

func (s *swapClient) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.stopForwarding()
	cur := s.cur
	s.mu.Unlock()
	close(s.events)
	close(s.states)
	return cur.Close()
}

// stopForwarding must be called with mu held.
func (s *swapClient) stopForwarding() {
	close(s.stop)
	<-s.done
}

// forward must be called with mu held. When connected is set, true is sent on
// States before any later state from c, so the program resyncs after a swap.
func (s *swapClient) forward(c Client, connected bool) {
	stop, done := make(chan struct{}), make(chan struct{})
	s.stop, s.done = stop, done
	go func() {
		defer close(done)
		events, states := c.Events(), c.States()
		var out chan bool
		var pending bool
		if connected {
			out, pending = s.states, true
		}
		for {
			select {
			case <-stop:
				return
			case out <- pending:
				out = nil
			case ev, ok := <-events:
				if !ok {
					return
				}
				select {
				case s.events <- ev:
				case <-stop:
					return
				}
			case st, ok := <-states:
				if !ok {
					return
				}
				if out != nil {
					pending = st
					continue
				}
				select {
				case s.states <- st:
				case <-stop:
					return
				}
			}
		}
	}()
}
