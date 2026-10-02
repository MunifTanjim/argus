package tui

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// inputFilterInterval is about one sample per 60 Hz frame, so that a mouse
// flood does not queue ahead of key presses.
const inputFilterInterval = 16 * time.Millisecond

// wheelMsg is the wheel after the input filter: the steps since the last one,
// negative up.
type wheelMsg struct {
	tea.Mouse
	delta int
}

type inputFilter struct {
	now   func() time.Time
	after func(time.Duration, func())
	send  func(tea.Msg)

	mu         sync.Mutex
	lastWheel  time.Time
	lastMotion time.Time
	wheel      int
	held       tea.Mouse
	flushing   bool
}

func newInputFilter() *inputFilter {
	return &inputFilter{
		now:   time.Now,
		after: func(d time.Duration, fn func()) { time.AfterFunc(d, fn) },
	}
}

func (f *inputFilter) filter(_ tea.Model, msg tea.Msg) tea.Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		d := 0
		switch msg.Button {
		case tea.MouseWheelUp:
			d = -1
		case tea.MouseWheelDown:
			d = 1
		default:
			return nil
		}
		if f.wheel*d < 0 {
			f.wheel = 0
		}
		f.wheel += d
		if !f.allow(&f.lastWheel) {
			f.held = msg.Mouse()
			if !f.flushing && f.send != nil {
				f.flushing = true
				f.after(inputFilterInterval-f.now().Sub(f.lastWheel), f.flush)
			}
			return nil
		}
		out := wheelMsg{Mouse: msg.Mouse(), delta: f.wheel}
		f.wheel = 0
		return out
	case tea.MouseMotionMsg:
		if !f.allow(&f.lastMotion) {
			return nil
		}
	}
	return msg
}

func (f *inputFilter) flush() {
	f.mu.Lock()
	f.flushing = false
	if f.wheel == 0 {
		f.mu.Unlock()
		return
	}
	out := wheelMsg{Mouse: f.held, delta: f.wheel}
	f.wheel = 0
	f.lastWheel = f.now()
	f.mu.Unlock()
	f.send(out)
}

func (f *inputFilter) allow(last *time.Time) bool {
	at := f.now()
	if !last.IsZero() && at.Sub(*last) < inputFilterInterval {
		return false
	}
	*last = at
	return true
}
