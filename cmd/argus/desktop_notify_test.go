package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/push"
)

type fakeFanouter struct {
	method string
	params json.RawMessage
	calls  int
}

func (f *fakeFanouter) Fanout(_ context.Context, method string, params json.RawMessage) []gateway.FanoutResult {
	f.method, f.params, f.calls = method, params, f.calls+1
	return nil
}

func TestDesktopClickCmd(t *testing.T) {
	cfg := &config.Config{Socket: "/tmp/argus.sock"}
	argv := desktopClickCmd(cfg)("nodeA:abc")
	// Must invoke `focus`, target the local socket, and carry the session id.
	joined := strings.Join(argv, " ")
	for _, want := range []string{"focus", "--socket", "/tmp/argus.sock", "nodeA:abc"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("click argv %v missing %q", argv, want)
		}
	}
}

func TestFanoutNotifierBroadcasts(t *testing.T) {
	fk := &fakeFanouter{}
	fn := fanoutNotifier{agg: fk, log: nil}
	fn.Notify(context.Background(), push.Notification{Title: "repo", Body: "Question: Auth"})

	if fk.calls != 1 || fk.method != api.MethodPushDesktop {
		t.Fatalf("calls=%d method=%q, want 1/%s", fk.calls, fk.method, api.MethodPushDesktop)
	}
	var n push.Notification
	if err := json.Unmarshal(fk.params, &n); err != nil {
		t.Fatalf("params not a Notification: %v", err)
	}
	if n.Body != "Question: Auth" {
		t.Fatalf("body = %q, want Question: Auth", n.Body)
	}
}
