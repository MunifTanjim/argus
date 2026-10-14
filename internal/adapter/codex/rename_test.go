package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// renameSession renames the thread through thread/name/set.
func TestRenameSessionCallsThreadNameSet(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "idle")}}
	d, f, _ := startDiscoverer(t, st)

	deadline := time.Now().Add(3 * time.Second)
	for d.conn() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if d.conn() == nil {
		t.Fatal("no daemon connection")
	}
	if err := d.renameSession(context.Background(), "t1", "New name"); err != nil {
		t.Fatalf("renameSession: %v", err)
	}
	m := f.expect(t, "thread/name/set")
	var p struct {
		ThreadID string `json:"threadId"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		t.Fatal(err)
	}
	if p.ThreadID != "t1" || p.Name != "New name" {
		t.Errorf("thread/name/set params = %s, want t1/New name", m.Params)
	}
}

func TestRenameSessionNeedsAConnection(t *testing.T) {
	d := newDiscoverer(nil, nil)
	if err := d.renameSession(context.Background(), "t1", "x"); err == nil {
		t.Error("want an error without a daemon connection")
	}
}
