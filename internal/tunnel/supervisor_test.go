package tunnel

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeProvider is a minimal Provider + LifecycleProvider for exercising the
// supervisor's Prepare handling without cloudflared.
type fakeProvider struct {
	spec       CommandSpec
	prepareURL string
	prepareErr error
}

func (f fakeProvider) Name() string                        { return "fake" }
func (f fakeProvider) Command(string) (CommandSpec, error) { return f.spec, nil }
func (f fakeProvider) ExtractURL(string) (string, bool)    { return "", false }
func (f fakeProvider) Prepare(context.Context) (string, error) {
	return f.prepareURL, f.prepareErr
}

// quietSupervisor returns a Supervisor with fast backoff and a discard logger so
// the fake binary's Info-level output stays out of the test stream.
func quietSupervisor() Supervisor {
	return Supervisor{
		Logger:     slog.New(slog.DiscardHandler),
		MinBackoff: time.Millisecond,
		MaxBackoff: time.Millisecond,
		KillGrace:  time.Second,
	}
}

// writeFakeBin writes script as an executable file in a temp dir and returns its
// path. Used to stand in for cloudflared without a network or real binary.
func writeFakeBin(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-cloudflared")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	return path
}

// classifyingProvider is a Provider + LineClassifier that reuses cloudflared's
// token parsing, for asserting the supervisor logs each line at its own severity.
type classifyingProvider struct{ bin string }

func (p classifyingProvider) Name() string { return "fake" }
func (p classifyingProvider) Command(string) (CommandSpec, error) {
	return CommandSpec{Path: p.bin}, nil
}
func (p classifyingProvider) ExtractURL(string) (string, bool) { return "", false }
func (p classifyingProvider) ClassifyLine(line string) slog.Level {
	return Cloudflare{}.ClassifyLine(line)
}

// capturingHandler records the level and "line" attr of each log record.
type capturingHandler struct {
	mu      sync.Mutex
	records []capturedRecord
}

type capturedRecord struct {
	level slog.Level
	line  string
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler            { return h }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	rec := capturedRecord{level: r.Level}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "line" {
			rec.line = a.Value.String()
		}
		return true
	})
	h.mu.Lock()
	h.records = append(h.records, rec)
	h.mu.Unlock()
	return nil
}

func (h *capturingHandler) levelFor(line string) (slog.Level, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.line == line {
			return r.level, true
		}
	}
	return 0, false
}

func TestSupervisorClassifiesLineLevels(t *testing.T) {
	bin := writeFakeBin(t, "#!/bin/sh\n"+
		"echo 'INF info line'\n"+
		"echo 'WRN warn line'\n"+
		"while true; do sleep 0.05; done\n")

	cap := &capturingHandler{}
	sup := Supervisor{Logger: slog.New(cap), MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond, KillGrace: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = sup.Run(ctx, classifyingProvider{bin: bin}, "http://127.0.0.1:8443", func(string) {})
		close(done)
	}()

	// Wait until both lines have been logged (or time out).
	deadline := time.After(3 * time.Second)
	for {
		_, gotInfo := cap.levelFor("INF info line")
		_, gotWarn := cap.levelFor("WRN warn line")
		if gotInfo && gotWarn {
			break
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("timed out waiting for both lines to be logged")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done

	if lvl, _ := cap.levelFor("INF info line"); lvl != slog.LevelInfo {
		t.Errorf("INF line logged at %v, want Info", lvl)
	}
	if lvl, _ := cap.levelFor("WRN warn line"); lvl != slog.LevelWarn {
		t.Errorf("WRN line logged at %v, want Warn", lvl)
	}
}

func TestSupervisorReportsURLThenStopsOnCancel(t *testing.T) {
	bin := writeFakeBin(t, "#!/bin/sh\n"+
		"echo 'INF |  https://fake-tunnel-1.trycloudflare.com  |'\n"+
		"while true; do sleep 0.05; done\n")

	sup := quietSupervisor()
	ctx, cancel := context.WithCancel(context.Background())

	urls := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- sup.Run(ctx, Cloudflare{Bin: bin}, "http://127.0.0.1:8443", func(u string) {
			select {
			case urls <- u:
			default:
			}
		})
	}()

	select {
	case u := <-urls:
		if u != "https://fake-tunnel-1.trycloudflare.com" {
			t.Fatalf("url = %q", u)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for url")
	}

	cancel()
	select {
	case err := <-done:
		if err == nil || err != context.Canceled {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestSupervisorRestartsOnEarlyExit(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	// Each run appends a byte then exits non-zero, forcing a restart.
	bin := writeFakeBin(t, "#!/bin/sh\nprintf x >> "+counter+"\nexit 1\n")

	sup := quietSupervisor()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- sup.Run(ctx, Cloudflare{Bin: bin}, "http://127.0.0.1:8443", func(string) {})
	}()

	// Poll until at least two runs have appended a byte, proving a restart, rather
	// than assuming a fixed wall-clock budget: a freshly-written binary's first
	// exec can take hundreds of ms (e.g. macOS Gatekeeper scans it), which would
	// otherwise race a tight timeout and kill the process before it ever runs.
	deadline := time.After(5 * time.Second)
	for {
		if data, _ := os.ReadFile(counter); len(data) >= 2 {
			break
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("timed out waiting for >= 2 runs")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestSupervisorReportsPreparedURL(t *testing.T) {
	bin := writeFakeBin(t, "#!/bin/sh\nwhile true; do sleep 0.05; done\n")
	p := fakeProvider{spec: CommandSpec{Path: bin}, prepareURL: "https://argus.example.com"}

	sup := quietSupervisor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	urls := make(chan string, 1)
	go func() {
		_ = sup.Run(ctx, p, "http://127.0.0.1:8443", func(u string) {
			select {
			case urls <- u:
			default:
			}
		})
	}()

	select {
	case u := <-urls:
		if u != "https://argus.example.com" {
			t.Fatalf("url = %q", u)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for prepared url")
	}
}

func TestSupervisorPrepareErrorAbortsRun(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "started")
	// If the run ever starts, the binary creates the sentinel file.
	bin := writeFakeBin(t, "#!/bin/sh\ntouch "+sentinel+"\n")
	p := fakeProvider{spec: CommandSpec{Path: bin}, prepareErr: errors.New("boom")}

	sup := quietSupervisor()
	err := sup.Run(context.Background(), p, "http://127.0.0.1:8443", func(string) {})
	if err == nil {
		t.Fatal("expected Prepare error to propagate")
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Error("run binary must not start when Prepare fails")
	}
}

func TestSupervisorReturnsErrorWhenBinaryMissing(t *testing.T) {
	sup := quietSupervisor()
	err := sup.Run(context.Background(), Cloudflare{Bin: "/nonexistent/cloudflared"}, "http://127.0.0.1:8443", func(string) {})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}
