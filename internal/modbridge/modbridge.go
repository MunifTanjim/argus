// Package modbridge holds the long polls a Claude Code mod keeps open to argusd
// and hands each command to one open poll, at most once.
package modbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Command kinds the mod runs.
const (
	KindPrompt   = "prompt"   // a prompt, or a slash command when it starts with "/"
	KindExit     = "exit"     // end the session
	KindFill     = "fill"     // put text in Claude's input box in place of the draft
	KindShell    = "shell"    // run "!" bash mode input in Claude's pane, keeping the draft
	KindCommands = "commands" // list Claude's slash commands; the ack carries them
)

// legacyKinds are what a mod that lists no kinds on its poll runs.
var legacyKinds = []string{KindPrompt, KindExit, KindFill, KindShell}

// FailBusy is the failure the mod acks for a fill Claude's input box refused
// because a dialog holds the keys.
const FailBusy = "busy"

// Command is one instruction for the mod. The mod acks it by ID once run, or
// once accepted when it is still waiting (e.g. on a running turn).
type Command struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// ErrUnreachable reports a session with no open poll and none recently closed.
var ErrUnreachable = errors.New("session has no mod connection")

// ErrNoAck reports a command handed to a poll that the mod never acked: the
// poll's client went away before the command reached Claude.
var ErrNoAck = errors.New("claude did not confirm the command")

// ErrBusy reports a command the mod refused with FailBusy.
var ErrBusy = errors.New("claude's input is held by a dialog")

// ErrUnsupported reports a command kind the session's mod does not run.
var ErrUnsupported = errors.New("claude's mod does not run this command")

// conn is one session's mod connection: an open poll, or the grace after one
// closed, during which the mod is expected to re-poll.
type conn struct {
	poll   chan Command  // buffered 1: a poll carries at most one command; nil between polls
	expiry *time.Timer   // armed between polls: the session is gone when it fires
	armed  uint64        // counts expiry arms, so a stale expiry is ignored
	opened chan struct{} // closed when a poll opens or the session expires; Send waits on it
	kinds  []string      // command kinds the mod of the last poll runs
}

// Bridge tracks one mod connection per session id.
type Bridge struct {
	grace      time.Duration
	ackTimeout time.Duration
	onChange   func(sessionID string)

	mu     sync.Mutex
	conns  map[string]*conn
	acks   map[string]chan ack
	lastID uint64
}

// New returns a Bridge. A session stays reachable for grace after its poll
// closes, since the mod re-polls at once. Send fails a command the mod does not
// ack within ackTimeout. onChange runs, outside the lock, whenever a session's
// reachability flips.
func New(grace, ackTimeout time.Duration, onChange func(sessionID string)) *Bridge {
	return &Bridge{
		grace:      grace,
		ackTimeout: ackTimeout,
		onChange:   onChange,
		conns:      map[string]*conn{},
		acks:       map[string]chan ack{},
	}
}

// Reachable reports whether sessionID has an open poll or closed one within grace.
func (b *Bridge) Reachable(sessionID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conns[sessionID] != nil
}

// Wait opens a poll for sessionID and returns the command handed to it, or none
// when ctx ends or hold elapses. kinds are the command kinds the polling mod
// runs. A newer poll for the session takes over its commands; the older one
// still waits out its hold, so two processes sharing a session id do not make
// each other re-poll in a tight loop.
func (b *Bridge) Wait(ctx context.Context, sessionID string, hold time.Duration, kinds []string) []Command {
	p := make(chan Command, 1)
	b.mu.Lock()
	c := b.conns[sessionID]
	was := c != nil
	if c == nil {
		c = &conn{}
		b.conns[sessionID] = c
	}
	c.poll = p
	c.kinds = kinds
	if len(kinds) == 0 {
		c.kinds = legacyKinds
	}
	if c.expiry != nil {
		c.expiry.Stop()
		c.expiry = nil
	}
	if c.opened != nil {
		close(c.opened)
		c.opened = nil
	}
	b.mu.Unlock()
	if !was {
		b.changed(sessionID)
	}

	timer := time.NewTimer(hold)
	defer timer.Stop()
	select {
	case cmd := <-p:
		return []Command{cmd}
	case <-ctx.Done():
	case <-timer.C:
	}
	b.mu.Lock()
	if c := b.conns[sessionID]; c != nil && c.poll == p {
		b.closeLocked(sessionID, c)
	}
	b.mu.Unlock()
	// Send may have handed a command over after the select returned; it is still
	// this poll's, and if the client is gone its Send fails with ErrNoAck.
	select {
	case cmd := <-p:
		return []Command{cmd}
	default:
		return nil
	}
}

// ack is the mod's answer to one command: its failure, or whether it ran
// rather than only being accepted, and any data it returned.
type ack struct {
	err  error
	ran  bool
	data json.RawMessage
}

// Send hands cmd to sessionID's open poll and waits for the mod's ack. Between
// polls (within grace) it waits for the mod to re-poll. It reports whether the
// command ran, as opposed to being accepted to run later. It returns
// ErrUnreachable when no poll comes, ErrNoAck when the mod never acks, and the
// mod's own failure when running the command failed.
func (b *Bridge) Send(ctx context.Context, sessionID string, cmd Command) (bool, error) {
	a, err := b.deliver(ctx, sessionID, cmd)
	return a.ran, err
}

// Request is Send for a command whose ack carries data, and returns that data.
func (b *Bridge) Request(ctx context.Context, sessionID string, cmd Command) (json.RawMessage, error) {
	a, err := b.deliver(ctx, sessionID, cmd)
	return a.data, err
}

func (b *Bridge) deliver(ctx context.Context, sessionID string, cmd Command) (ack, error) {
	for {
		b.mu.Lock()
		c := b.conns[sessionID]
		if c == nil {
			b.mu.Unlock()
			return ack{}, ErrUnreachable
		}
		// Checked on every pass: a re-poll may come from an older mod.
		if !slices.Contains(c.kinds, cmd.Kind) {
			b.mu.Unlock()
			return ack{}, ErrUnsupported
		}
		if p := c.poll; p != nil {
			b.lastID++
			cmd.ID = strconv.FormatUint(b.lastID, 10)
			ch := make(chan ack, 1)
			b.acks[cmd.ID] = ch
			b.closeLocked(sessionID, c)
			p <- cmd
			b.mu.Unlock()
			return b.awaitAck(ctx, cmd.ID, ch)
		}
		if c.opened == nil {
			c.opened = make(chan struct{})
		}
		opened := c.opened
		b.mu.Unlock()

		select {
		case <-opened:
		case <-ctx.Done():
			return ack{}, ctx.Err()
		}
	}
}

func (b *Bridge) awaitAck(ctx context.Context, id string, ch chan ack) (ack, error) {
	defer func() {
		b.mu.Lock()
		delete(b.acks, id)
		b.mu.Unlock()
	}()
	t := time.NewTimer(b.ackTimeout)
	defer t.Stop()
	select {
	case a := <-ch:
		return a, a.err
	case <-t.C:
		return ack{}, ErrNoAck
	case <-ctx.Done():
		return ack{}, ctx.Err()
	}
}

// Ack completes the Send or Request of command id. A non-empty failure is the
// mod's error from running it; ran reports that it ran rather than only being
// accepted; data is what Request returns. An unknown or already completed id is
// ignored.
func (b *Bridge) Ack(id, failure string, ran bool, data json.RawMessage) {
	b.mu.Lock()
	ch := b.acks[id]
	delete(b.acks, id)
	b.mu.Unlock()
	if ch == nil {
		return
	}
	if failure == FailBusy {
		ch <- ack{err: ErrBusy}
		return
	}
	if failure != "" {
		ch <- ack{err: fmt.Errorf("claude: %s", failure)}
		return
	}
	ch <- ack{ran: ran, data: data}
}

func (b *Bridge) closeLocked(sessionID string, c *conn) {
	c.poll = nil
	if c.expiry != nil {
		c.expiry.Stop()
	}
	c.armed++
	armed := c.armed
	c.expiry = time.AfterFunc(b.grace, func() { b.expire(sessionID, c, armed) })
}

// expire ends sessionID's connection, unless a poll opened or a newer expiry
// was armed since this one.
func (b *Bridge) expire(sessionID string, c *conn, armed uint64) {
	b.mu.Lock()
	if b.conns[sessionID] != c || c.poll != nil || c.armed != armed {
		b.mu.Unlock()
		return
	}
	delete(b.conns, sessionID)
	if c.opened != nil {
		close(c.opened)
	}
	b.mu.Unlock()
	b.changed(sessionID)
}

func (b *Bridge) changed(sessionID string) {
	if b.onChange != nil {
		b.onChange(sessionID)
	}
}
