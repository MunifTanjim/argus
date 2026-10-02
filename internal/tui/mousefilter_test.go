package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type filterHarness struct {
	*inputFilter
	now       time.Time
	scheduled []func()
	delays    []time.Duration
	sent      []tea.Msg
}

func newHarness() *filterHarness {
	h := &filterHarness{now: time.Now()}
	h.inputFilter = &inputFilter{
		now: func() time.Time { return h.now },
		after: func(d time.Duration, fn func()) {
			h.delays = append(h.delays, d)
			h.scheduled = append(h.scheduled, fn)
		},
		send: func(m tea.Msg) { h.sent = append(h.sent, m) },
	}
	return h
}

func testFilter() (*inputFilter, *time.Time) {
	h := newHarness()
	return h.inputFilter, &h.now
}

func wheel(b tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{X: 4, Y: 5, Button: b}
}

func TestFilterPassesTheFirstWheel(t *testing.T) {
	f, _ := testFilter()
	got, ok := f.filter(nil, wheel(tea.MouseWheelDown)).(wheelMsg)
	if !ok || got.delta != 1 || got.X != 4 || got.Y != 5 {
		t.Fatalf("got %#v, want a wheelMsg of +1 at 4,5", got)
	}
}

func TestFilterSumsWheelInOneWindow(t *testing.T) {
	f, now := testFilter()
	f.filter(nil, wheel(tea.MouseWheelDown))
	for range 2 {
		*now = now.Add(5 * time.Millisecond)
		if got := f.filter(nil, wheel(tea.MouseWheelDown)); got != nil {
			t.Fatalf("a wheel inside the window must be held, got %#v", got)
		}
	}
	*now = now.Add(20 * time.Millisecond)
	got, ok := f.filter(nil, wheel(tea.MouseWheelDown)).(wheelMsg)
	if !ok || got.delta != 3 {
		t.Fatalf("got %#v, want the sum +3", got)
	}
}

func TestFilterResetsOnDirectionChange(t *testing.T) {
	f, now := testFilter()
	f.filter(nil, wheel(tea.MouseWheelDown))
	*now = now.Add(5 * time.Millisecond)
	f.filter(nil, wheel(tea.MouseWheelDown))
	*now = now.Add(20 * time.Millisecond)
	got, ok := f.filter(nil, wheel(tea.MouseWheelUp)).(wheelMsg)
	if !ok || got.delta != -1 {
		t.Fatalf("got %#v, want -1 after the direction change", got)
	}
}

func TestFilterDropsSidewaysWheel(t *testing.T) {
	f, _ := testFilter()
	if got := f.filter(nil, wheel(tea.MouseWheelLeft)); got != nil {
		t.Fatalf("got %#v, want nil", got)
	}
}

func TestFilterThrottlesMotionApartFromWheel(t *testing.T) {
	f, now := testFilter()
	motion := tea.MouseMotionMsg{X: 1, Y: 1, Button: tea.MouseLeft}
	if f.filter(nil, motion) == nil {
		t.Fatal("the first motion must pass")
	}
	*now = now.Add(5 * time.Millisecond)
	if f.filter(nil, motion) != nil {
		t.Fatal("a motion inside the window must be dropped")
	}
	if _, ok := f.filter(nil, wheel(tea.MouseWheelUp)).(wheelMsg); !ok {
		t.Fatal("the wheel has its own window")
	}
}

func TestFilterPassesOtherMessages(t *testing.T) {
	f, _ := testFilter()
	click := tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft}
	if got := f.filter(nil, click); got != click {
		t.Fatalf("got %#v, want the click unchanged", got)
	}
}

func TestFilterFlushesHeldWheelAtWindowEnd(t *testing.T) {
	h := newHarness()
	h.filter(nil, wheel(tea.MouseWheelDown))
	for range 2 {
		h.now = h.now.Add(5 * time.Millisecond)
		h.filter(nil, wheel(tea.MouseWheelDown))
	}
	if len(h.scheduled) != 1 {
		t.Fatalf("scheduled %d flushes, want 1", len(h.scheduled))
	}
	if h.delays[0] != inputFilterInterval-5*time.Millisecond {
		t.Fatalf("delay = %v", h.delays[0])
	}
	h.scheduled[0]()
	if len(h.sent) != 1 {
		t.Fatalf("sent %#v, want one wheelMsg", h.sent)
	}
	got, ok := h.sent[0].(wheelMsg)
	if !ok || got.delta != 2 || got.X != 4 || got.Y != 5 {
		t.Fatalf("got %#v, want +2 at 4,5", h.sent[0])
	}
	h.scheduled[0]()
	if len(h.sent) != 1 {
		t.Fatalf("an empty flush must send nothing, sent %#v", h.sent)
	}
}

func TestFilterSchedulesOneFlushPerWindow(t *testing.T) {
	h := newHarness()
	h.filter(nil, wheel(tea.MouseWheelDown))
	for range 3 {
		h.now = h.now.Add(2 * time.Millisecond)
		h.filter(nil, wheel(tea.MouseWheelDown))
	}
	if len(h.scheduled) != 1 {
		t.Fatalf("scheduled %d flushes, want 1", len(h.scheduled))
	}
}

func TestFilterFlushAfterDirectionChange(t *testing.T) {
	h := newHarness()
	h.filter(nil, wheel(tea.MouseWheelDown))
	h.now = h.now.Add(2 * time.Millisecond)
	h.filter(nil, wheel(tea.MouseWheelDown))
	h.now = h.now.Add(2 * time.Millisecond)
	h.filter(nil, wheel(tea.MouseWheelUp))
	h.scheduled[0]()
	got, ok := h.sent[0].(wheelMsg)
	if !ok || got.delta != -1 {
		t.Fatalf("got %#v, want -1", h.sent[0])
	}
}

func TestFilterEmitsNormallyAfterFlush(t *testing.T) {
	h := newHarness()
	h.filter(nil, wheel(tea.MouseWheelDown))
	h.now = h.now.Add(5 * time.Millisecond)
	h.filter(nil, wheel(tea.MouseWheelDown))
	h.now = h.now.Add(11 * time.Millisecond)
	h.scheduled[0]()
	h.now = h.now.Add(20 * time.Millisecond)
	got, ok := h.filter(nil, wheel(tea.MouseWheelDown)).(wheelMsg)
	if !ok || got.delta != 1 {
		t.Fatalf("got %#v, want +1", got)
	}
}

func TestFilterPassesWheelMsgThrough(t *testing.T) {
	f, _ := testFilter()
	in := wheelMsg{delta: 2}
	if got := f.filter(nil, in); got != in {
		t.Fatalf("got %#v, want unchanged", got)
	}
}
