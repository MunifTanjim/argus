package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
)

type capRec struct {
	msg   string
	attrs map[string]any
}

type capHandler struct {
	mu   sync.Mutex
	recs []capRec
}

func (h *capHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *capHandler) WithGroup(string) slog.Handler            { return h }
func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	rec := capRec{msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool { rec.attrs[a.Key] = a.Value.Any(); return true })
	h.mu.Lock()
	h.recs = append(h.recs, rec)
	h.mu.Unlock()
	return nil
}

func TestServerRequestLogging(t *testing.T) {
	cap := &capHandler{}
	s := NewServer()
	s.SetLogger(slog.New(cap))
	s.Handle("echo", func(context.Context, json.RawMessage) (any, error) { return "ok", nil })
	s.Handle("boom", func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("nope") })

	df := s.DispatchFunc()
	_, _ = df(context.Background(), "echo", nil)
	_, _ = df(context.Background(), "boom", nil)
	_, _ = df(context.Background(), "missing", nil) // method-not-found

	if len(cap.recs) != 3 {
		t.Fatalf("want 3 log records, got %d", len(cap.recs))
	}
	for i, want := range []string{"echo", "boom", "missing"} {
		r := cap.recs[i]
		if r.attrs["method"] != want {
			t.Errorf("record %d method = %v, want %q", i, r.attrs["method"], want)
		}
		if _, ok := r.attrs["dur"]; !ok {
			t.Errorf("record %d missing dur attr", i)
		}
	}
	if _, ok := cap.recs[0].attrs["err"]; ok {
		t.Error("successful echo should have no err attr")
	}
	if _, ok := cap.recs[1].attrs["err"]; !ok {
		t.Error("failed boom should carry an err attr")
	}
	if _, ok := cap.recs[2].attrs["err"]; !ok {
		t.Error("method-not-found should carry an err attr")
	}
}

func TestServerNoLoggerSilent(t *testing.T) {
	s := NewServer()
	s.Handle("echo", func(context.Context, json.RawMessage) (any, error) { return "ok", nil })
	// No SetLogger: dispatch must work and not panic.
	if _, err := s.DispatchFunc()(context.Background(), "echo", nil); err != nil {
		t.Fatalf("dispatch with no logger: %v", err)
	}
}
