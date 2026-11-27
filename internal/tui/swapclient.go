package tui

import (
	"sync"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
)

// swapClient lets the TUI replace its connection while it runs. Events,
// States and Paths stay the same channels across swaps; only one forwarder goroutine
// writes to them at a time.
type swapClient struct {
	events chan api.Notification
	states chan bool
	paths  chan client.NodePath

	// vias is the nodes the forwarder has reported a link for. Only the
	// forwarder writes it; swap reads it once the forwarder has stopped.
	vias map[string]bool

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
		paths:  make(chan client.NodePath),
		vias:   map[string]bool{},
		cur:    c,
	}
	s.forward(c, false, nil)
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
func (s *swapClient) Paths() <-chan client.NodePath   { return s.paths }
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
	// The new client reports its own links; the old one's are forgotten.
	var resets []client.NodePath
	for id := range s.vias {
		resets = append(resets, client.NodePath{NodeID: id})
	}
	clear(s.vias)
	s.forward(next, true, resets)
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
	close(s.paths)
	return cur.Close()
}

// stopForwarding must be called with mu held.
func (s *swapClient) stopForwarding() {
	close(s.stop)
	<-s.done
}

// forward must be called with mu held. When connected is set, true is sent on
// States before any later state from c, so the program resyncs after a swap.
// resets are sent on Paths before any path from c.
func (s *swapClient) forward(c Client, connected bool, resets []client.NodePath) {
	stop, done := make(chan struct{}), make(chan struct{})
	s.stop, s.done = stop, done
	go func() {
		defer close(done)
		events, states, paths := c.Events(), c.States(), c.Paths()
		var out chan bool
		var pending bool
		if connected {
			out, pending = s.states, true
		}
		for _, r := range resets {
			select {
			case s.paths <- r:
			case <-stop:
				return
			}
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
			case np, ok := <-paths:
				if !ok {
					paths = nil
					continue
				}
				select {
				case s.paths <- np:
					if np.Via == "" {
						delete(s.vias, np.NodeID)
					} else {
						s.vias[np.NodeID] = true
					}
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
