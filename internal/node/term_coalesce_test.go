package node

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

type coalesceHarness struct {
	in      chan []byte
	timers  chan chan time.Time
	flushed chan []byte
	done    chan error
}

func newCoalesceHarness(max int, flushErr error) *coalesceHarness {
	h := &coalesceHarness{
		in:      make(chan []byte),
		timers:  make(chan chan time.Time, 16),
		flushed: make(chan []byte, 16),
		done:    make(chan error, 1),
	}
	after := func(time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		h.timers <- c
		return c
	}
	flush := func(b []byte) error {
		h.flushed <- append([]byte(nil), b...)
		return flushErr
	}
	go func() { h.done <- coalesce(h.in, flush, time.Millisecond, max, after) }()
	return h
}

func (h *coalesceHarness) expectFlush(t *testing.T, want []byte) {
	t.Helper()
	select {
	case got := <-h.flushed:
		if !bytes.Equal(got, want) {
			t.Fatalf("flush = %q (len %d), want %q (len %d)", got, len(got), want, len(want))
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no flush, want %q", want)
	}
}

func (h *coalesceHarness) expectNoFlush(t *testing.T) {
	t.Helper()
	select {
	case got := <-h.flushed:
		t.Fatalf("unexpected flush %q", got)
	default:
	}
}

func (h *coalesceHarness) fire(t *testing.T) {
	t.Helper()
	select {
	case c := <-h.timers:
		c <- time.Now()
	case <-time.After(2 * time.Second):
		t.Fatal("timer not armed")
	}
}

// send delivers a chunk; the unbuffered channel guarantees the previous chunk
// was received (and handled) before the loop accepts this one.
func (h *coalesceHarness) send(b string) { h.in <- []byte(b) }

func TestCoalesceLeadingEdge(t *testing.T) {
	h := newCoalesceHarness(1024, nil)
	h.send("a")
	h.expectFlush(t, []byte("a"))
	close(h.in)
	if err := <-h.done; err != nil {
		t.Fatal(err)
	}
	h.expectNoFlush(t)
}

func TestCoalesceBurst(t *testing.T) {
	h := newCoalesceHarness(1024, nil)
	h.send("a")
	h.expectFlush(t, []byte("a"))
	h.send("b")
	h.send("c")
	h.expectNoFlush(t)
	h.fire(t)
	h.expectFlush(t, []byte("bc"))
}

func TestCoalesceMax(t *testing.T) {
	h := newCoalesceHarness(32768, nil)
	h.send("a")
	h.expectFlush(t, []byte("a"))
	h.in <- bytes.Repeat([]byte("x"), 20*1024)
	h.in <- bytes.Repeat([]byte("y"), 20*1024)
	h.expectFlush(t, append(bytes.Repeat([]byte("x"), 20*1024), bytes.Repeat([]byte("y"), 12*1024)...))
	h.expectNoFlush(t)
	h.fire(t) // timer armed after first flush
	h.expectFlush(t, bytes.Repeat([]byte("y"), 8*1024))
}

func TestCoalesceIdleAgain(t *testing.T) {
	h := newCoalesceHarness(1024, nil)
	h.send("a")
	h.expectFlush(t, []byte("a"))
	h.fire(t) // empty buffer: back to idle
	h.send("b")
	h.expectFlush(t, []byte("b"))
}

func TestCoalesceFlushOnClose(t *testing.T) {
	h := newCoalesceHarness(1024, nil)
	h.send("a")
	h.expectFlush(t, []byte("a"))
	h.send("x")
	close(h.in)
	if err := <-h.done; err != nil {
		t.Fatal(err)
	}
	h.expectFlush(t, []byte("x"))
}

func TestCoalesceFlushError(t *testing.T) {
	boom := errors.New("boom")
	h := newCoalesceHarness(1024, boom)
	h.send("a")
	if err := <-h.done; err != boom {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestCoalescePreservesBytes(t *testing.T) {
	h := newCoalesceHarness(1024, nil)
	h.send("\xc3")
	h.expectFlush(t, []byte{0xc3})
	h.send("\xa9")
	close(h.in)
	<-h.done
	h.expectFlush(t, []byte{0xa9})
}
