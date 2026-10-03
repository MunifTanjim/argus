package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/atomicfile"
)

var (
	ErrWakelockUnsupported = errors.New("wakelock is not supported on this node")
	ErrInvalidUntil        = errors.New("invalid until")
)

type process interface {
	Kill() error
	Wait() error
}

// starter holds the wakelock in a child process for secs seconds, or until
// killed when secs is 0.
type starter func(secs int64) (process, error)

// Wakelock keeps the host awake while its child process lives. The child's
// exit clears the state after a timeout or an unexpected exit; Set clears it
// synchronously.
type Wakelock struct {
	path  string
	start starter
	now   func() time.Time
	log   *slog.Logger

	mu    sync.Mutex
	until string
	proc  process
	gen   uint64
}

// NewWakelock persists the state at path; an empty path keeps it in memory.
func NewWakelock(path string) *Wakelock {
	return &Wakelock{path: path, start: defaultStarter(), now: time.Now, log: slog.New(slog.DiscardHandler)}
}

func (w *Wakelock) SetLogger(l *slog.Logger) { w.log = l }

func (w *Wakelock) State() api.HostWakelock {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stateLocked()
}

func (w *Wakelock) stateLocked() api.HostWakelock {
	return api.HostWakelock{Until: w.until}
}

func (w *Wakelock) Supported() bool { return w.start != nil }

func (w *Wakelock) Set(until string) (api.HostWakelock, error) {
	if !w.Supported() {
		return api.HostWakelock{}, ErrWakelockUnsupported
	}
	var secs int64
	if until != "" && until != api.HostWakelockIndefinite {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			return api.HostWakelock{}, fmt.Errorf("%w %q", ErrInvalidUntil, until)
		}
		d := t.Sub(w.now())
		if d <= 0 {
			return api.HostWakelock{}, fmt.Errorf("%w: %s is in the past", ErrInvalidUntil, until)
		}
		secs = int64(math.Ceil(d.Seconds()))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	if until != "" {
		p, err := w.start(secs)
		if err != nil {
			w.saveLocked()
			return w.stateLocked(), err
		}
		w.proc, w.until = p, until
		go w.wait(p, w.gen)
	}
	w.saveLocked()
	return w.stateLocked(), nil
}

// Restore takes the saved wakelock again after a restart.
func (w *Wakelock) Restore() {
	if w.path == "" || !w.Supported() {
		return
	}
	b, err := os.ReadFile(w.path)
	if err != nil {
		return
	}
	var s struct {
		Until string `json:"until"`
	}
	if json.Unmarshal(b, &s) != nil || s.Until == "" {
		return
	}
	if _, err := w.Set(s.Until); err != nil {
		w.log.Info("wakelock not restored", "until", s.Until, "err", err)
		w.mu.Lock()
		w.saveLocked()
		w.mu.Unlock()
	}
}

// stopLocked bumps gen first, so the killed child's wait does not clear the
// state of the child that replaces it.
func (w *Wakelock) stopLocked() {
	w.gen++
	if w.proc != nil {
		if err := w.proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			w.log.Warn("kill wakelock child", "err", err)
		}
		w.proc = nil
	}
	w.until = ""
}

func (w *Wakelock) wait(p process, gen uint64) {
	err := p.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	if gen != w.gen {
		return
	}
	w.log.Info("wakelock released", "until", w.until, "err", err)
	w.proc, w.until = nil, ""
	w.saveLocked()
}

func (w *Wakelock) saveLocked() {
	if w.path == "" {
		return
	}
	b, _ := json.Marshal(struct {
		Until string `json:"until"`
	}{w.until})
	if err := atomicfile.Write(w.path, b); err != nil {
		w.log.Warn("save wakelock", "err", err)
	}
}
