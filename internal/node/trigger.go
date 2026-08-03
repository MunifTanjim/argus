package node

import (
	"sync"
	"time"
)

// triggerLimiter runs an untrusted party's request at most once per interval.
// A request that arrives inside the window is deferred to the end of it, never
// dropped: two operator changes seconds apart must both be acted on, not one of
// them left to the multi-minute backstop. Deferred requests coalesce into a single
// run, so a flood still costs one run per window rather than a queue of them.
//
// The zero value is ready to use. fn must be the same function for every request
// on a given limiter; it is stashed so the deferred run can call it.
type triggerLimiter struct {
	mu       sync.Mutex
	fn       func()
	last     time.Time
	inFlight bool
	pending  bool
	timer    *time.Timer
}

func (l *triggerLimiter) request(interval time.Duration, fn func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fn = fn
	if l.pending {
		return
	}
	if l.inFlight {
		l.pending = true // re-armed when the running one finishes
		return
	}
	if wait := interval - time.Since(l.last); wait > 0 {
		l.pending = true
		if l.timer != nil {
			l.timer.Stop()
		}
		l.timer = time.AfterFunc(wait, func() { l.fire(interval) })
		return
	}
	l.startLocked(interval)
}

func (l *triggerLimiter) fire(interval time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.pending || l.inFlight {
		return
	}
	l.pending = false
	l.startLocked(interval)
}

// startLocked runs fn on its own goroutine: requests are dispatched on a peer's
// read loop, and fn issues RPCs only that loop can answer. Caller holds mu.
func (l *triggerLimiter) startLocked(interval time.Duration) {
	l.last = time.Now()
	l.inFlight = true
	fn := l.fn
	go func() {
		defer l.finish(interval)
		fn()
	}()
}

func (l *triggerLimiter) finish(interval time.Duration) {
	l.mu.Lock()
	l.inFlight = false
	pending := l.pending
	l.pending = false
	fn := l.fn
	l.mu.Unlock()
	if pending {
		l.request(interval, fn)
	}
}

// stop cancels a deferred run. Used by tests.
func (l *triggerLimiter) stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending = false
	if l.timer != nil {
		l.timer.Stop()
	}
}

// idle reports that nothing is running or owed.
func (l *triggerLimiter) idle() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.inFlight && !l.pending
}

// backdate moves the last-run stamp back by d so a test can reopen the window
// without sleeping through it.
func (l *triggerLimiter) backdate(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last = l.last.Add(-d)
}
