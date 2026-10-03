//go:build codex_live

package codex

import (
	"context"
	"testing"
	"time"
)

// Run with: go test ./internal/adapter/codex/ -tags codex_live -run TestLiveSmoke -v
func TestLiveSmoke(t *testing.T) {
	path, err := daemonSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := dialRPC(ctx, path)
	if err != nil {
		t.Skipf("codex daemon not reachable at %s: %v", path, err)
	}
	defer c.close()
	if err := c.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	var loaded struct {
		Data []string `json:"data"`
	}
	if err := c.call(ctx, "thread/loaded/list", map[string]any{}, &loaded); err != nil {
		t.Fatalf("thread/loaded/list: %v", err)
	}
	t.Logf("loaded threads: %d", len(loaded.Data))
	for _, id := range loaded.Data {
		var res struct {
			Thread cxThread `json:"thread"`
		}
		if err := c.call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, &res); err != nil {
			t.Errorf("thread/read %s: %v", id, err)
			continue
		}
		t.Logf("%s trackable=%v title=%q status=%+v", id, res.Thread.trackable(), res.Thread.title(), res.Thread.Status)
	}
}
