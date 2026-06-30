// Package logbuf is a bounded, concurrency-safe in-memory ring of log lines.
// The TUI attaches it to the embedded node's logger (via an io.Writer-backed
// slog handler) so the node's operational output can be tailed in a Logs tab
// instead of being discarded. It implements io.Writer: each newline-terminated
// record written becomes one stored line.
package logbuf

import (
	"bytes"
	"sync"
)

// Buffer retains the most recent lines written to it, dropping the oldest once
// it exceeds max. Safe for concurrent writers (the logger) and readers (the TUI).
type Buffer struct {
	mu     sync.Mutex
	lines  []string
	carry  []byte // bytes of an as-yet-unterminated trailing line
	max    int
	notify chan struct{}
}

// New returns a Buffer holding at most max lines (max <= 0 falls back to 1).
func New(max int) *Buffer {
	if max <= 0 {
		max = 1
	}
	return &Buffer{max: max, notify: make(chan struct{}, 1)}
}

// Write appends p, splitting on newlines into stored lines. A trailing partial
// line (no newline yet) is held until a later Write completes it. It never errors
// and always reports len(p) consumed, so it is a well-behaved io.Writer for slog.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.carry = append(b.carry, p...)
	for {
		i := bytes.IndexByte(b.carry, '\n')
		if i < 0 {
			break
		}
		b.lines = append(b.lines, string(b.carry[:i]))
		b.carry = b.carry[i+1:]
		if len(b.lines) > b.max {
			b.lines = b.lines[len(b.lines)-b.max:]
		}
	}
	b.mu.Unlock()
	b.signal()
	return len(p), nil
}

// signal does a non-blocking send so bursts of writes coalesce into one wake-up
// and a writing goroutine never blocks on a slow reader.
func (b *Buffer) signal() {
	select {
	case b.notify <- struct{}{}:
	default:
	}
}

// Lines returns a snapshot copy of the buffered lines, oldest first.
func (b *Buffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

// LinesRange returns a snapshot copy of up to n lines starting at offset off
// (clamped to the buffer). The TUI uses it to copy only the visible window each
// render instead of the whole ring.
func (b *Buffer) LinesRange(off, n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	off = max(0, min(off, len(b.lines)))
	end := min(len(b.lines), off+max(0, n))
	out := make([]string, end-off)
	copy(out, b.lines[off:end])
	return out
}

// Len reports the number of complete buffered lines.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// Notify yields a value shortly after lines are appended. Sends are coalesced
// (single-slot channel); use it only as a "something changed" signal and read
// the content via Lines.
func (b *Buffer) Notify() <-chan struct{} { return b.notify }
