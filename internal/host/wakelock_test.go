package host

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

type fakeProc struct {
	done   chan struct{}
	once   sync.Once
	killed bool
}

func newFakeProc() *fakeProc { return &fakeProc{done: make(chan struct{})} }

func (p *fakeProc) Kill() error { p.killed = true; p.exit(); return nil }
func (p *fakeProc) Wait() error { <-p.done; return nil }
func (p *fakeProc) exit()       { p.once.Do(func() { close(p.done) }) }

type fakeStarter struct {
	mu    sync.Mutex
	secs  []int64
	procs []*fakeProc
}

func (f *fakeStarter) start(secs int64) (process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := newFakeProc()
	f.secs = append(f.secs, secs)
	f.procs = append(f.procs, p)
	return p, nil
}

var testNow = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func newTestWakelock(t *testing.T, path string) (*Wakelock, *fakeStarter) {
	t.Helper()
	f := &fakeStarter{}
	w := NewWakelock(path)
	w.start = f.start
	w.now = func() time.Time { return testNow }
	return w, f
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func savedUntil(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWakelockTimed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wakelock.json")
	w, f := newTestWakelock(t, path)
	until := testNow.Add(30 * time.Minute).Format(time.RFC3339)
	got, err := w.Set(until)
	if err != nil {
		t.Fatal(err)
	}
	if got != (api.HostWakelock{Until: until}) {
		t.Fatalf("state %+v", got)
	}
	if len(f.secs) != 1 || f.secs[0] != 1800 {
		t.Fatalf("secs %v, want [1800]", f.secs)
	}
	if s := savedUntil(t, path); s != `{"until":"`+until+`"}` {
		t.Fatalf("saved %s", s)
	}
	f.procs[0].exit()
	eventually(t, func() bool { return w.State().Until == "" })
	if s := savedUntil(t, path); s != `{"until":""}` {
		t.Fatalf("saved after exit %s", s)
	}
}

func TestWakelockIndefinite(t *testing.T) {
	w, f := newTestWakelock(t, "")
	if _, err := w.Set(api.HostWakelockIndefinite); err != nil {
		t.Fatal(err)
	}
	if f.secs[0] != 0 {
		t.Fatalf("secs %v, want [0] (no -t)", f.secs)
	}
}

func TestWakelockOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wakelock.json")
	w, f := newTestWakelock(t, path)
	if _, err := w.Set(api.HostWakelockIndefinite); err != nil {
		t.Fatal(err)
	}
	got, err := w.Set("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Until != "" || !f.procs[0].killed {
		t.Fatalf("state %+v killed %v", got, f.procs[0].killed)
	}
	if s := savedUntil(t, path); s != `{"until":""}` {
		t.Fatalf("saved after off %s", s)
	}
	time.Sleep(20 * time.Millisecond)
	if s := savedUntil(t, path); s != `{"until":""}` {
		t.Fatalf("saved after waiter %s", s)
	}
}

func TestWakelockReplacedChildKeepsNewState(t *testing.T) {
	w, f := newTestWakelock(t, "")
	if _, err := w.Set(api.HostWakelockIndefinite); err != nil {
		t.Fatal(err)
	}
	until := testNow.Add(time.Hour).Format(time.RFC3339)
	if _, err := w.Set(until); err != nil {
		t.Fatal(err)
	}
	if !f.procs[0].killed {
		t.Fatal("first child not killed")
	}
	time.Sleep(20 * time.Millisecond)
	if got := w.State().Until; got != until {
		t.Fatalf("until %q, want %q", got, until)
	}
}

func TestWakelockRejects(t *testing.T) {
	w, f := newTestWakelock(t, "")
	for _, until := range []string{"tomorrow", testNow.Add(-time.Minute).Format(time.RFC3339), testNow.Format(time.RFC3339)} {
		if _, err := w.Set(until); !errors.Is(err, ErrInvalidUntil) {
			t.Errorf("Set(%q) err %v, want ErrInvalidUntil", until, err)
		}
	}
	if len(f.secs) != 0 {
		t.Fatalf("started %v", f.secs)
	}
	w.start = nil
	if _, err := w.Set(""); !errors.Is(err, ErrWakelockUnsupported) {
		t.Fatalf("err %v, want ErrWakelockUnsupported", err)
	}
	if w.Supported() {
		t.Fatal("Supported must be false without a starter")
	}
}

func TestWakelockRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wakelock.json")
	until := testNow.Add(10 * time.Minute).Format(time.RFC3339)
	if err := os.WriteFile(path, []byte(`{"until":"`+until+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	w, f := newTestWakelock(t, path)
	w.Restore()
	if w.State().Until != until || len(f.secs) != 1 || f.secs[0] != 600 {
		t.Fatalf("state %+v secs %v", w.State(), f.secs)
	}
}

func TestWakelockRestoreExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wakelock.json")
	past := testNow.Add(-time.Minute).Format(time.RFC3339)
	if err := os.WriteFile(path, []byte(`{"until":"`+past+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	w, f := newTestWakelock(t, path)
	w.Restore()
	if w.State().Until != "" || len(f.secs) != 0 {
		t.Fatalf("state %+v secs %v", w.State(), f.secs)
	}
	if s := savedUntil(t, path); s != `{"until":""}` {
		t.Fatalf("saved %s", s)
	}
}

func TestWakelockStartErrorIsNotInvalidUntil(t *testing.T) {
	w, _ := newTestWakelock(t, "")
	startErr := errors.New("exec failed")
	w.start = func(int64) (process, error) { return nil, startErr }
	_, err := w.Set(api.HostWakelockIndefinite)
	if !errors.Is(err, startErr) || errors.Is(err, ErrInvalidUntil) {
		t.Fatalf("err %v", err)
	}
}
