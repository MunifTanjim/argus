package selfupgrade

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

func dispatch(t *testing.T, s *Service, ctx context.Context, method string) (any, error) {
	t.Helper()
	srv := api.NewServer()
	s.Register(srv)
	return srv.DispatchFunc()(ctx, method, json.RawMessage(`{"node_id":"n1"}`))
}

func TestServiceCheck(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	s := NewService(testUpdater(t, srv, "argus"), func() {})
	res, err := dispatch(t, s, context.Background(), api.MethodServerCheckUpgrade)
	if err != nil || res != (api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"}) {
		t.Fatalf("checkUpgrade = %+v, %v", res, err)
	}
}

func TestServiceCheckFresh(t *testing.T) {
	f, srv := newFakeRelease(t, "1.1.0", newBinary)
	s := NewService(testUpdater(t, srv, "argus"), func() {})
	server := api.NewServer()
	s.Register(server)
	check := func(params string) api.CheckUpgradeResult {
		t.Helper()
		res, err := server.DispatchFunc()(context.Background(), api.MethodServerCheckUpgrade, json.RawMessage(params))
		if err != nil {
			t.Fatalf("checkUpgrade(%q): %v", params, err)
		}
		return res.(api.CheckUpgradeResult)
	}
	check(``)
	f.tag = "1.2.0"
	if got := check(``).Latest; got != "1.1.0" {
		t.Fatalf("cached Latest = %q, want 1.1.0", got)
	}
	if got := check(`{"fresh":true}`).Latest; got != "1.2.0" {
		t.Fatalf("fresh Latest = %q, want 1.2.0", got)
	}
}

func TestUpgradeSurvivesCancelledRequest(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	s := NewService(u, func() {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := dispatch(t, s, ctx, api.MethodServerUpgrade)
	if err != nil || res != (api.UpgradeResult{From: "1.0.0", To: "1.1.0"}) {
		t.Fatalf("upgrade with a cancelled request = %+v, %v", res, err)
	}
	if got := readFile(t, u.ExecPath); got != string(newBinary) {
		t.Fatal("binary not replaced")
	}
}

func TestRestartRunsAfterReply(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	restarted := make(chan struct{})
	s := NewService(testUpdater(t, srv, "argus"), func() { close(restarted) })
	s.restartDelay = 10 * time.Millisecond
	if _, err := dispatch(t, s, context.Background(), api.MethodServerRestart); err != nil {
		t.Fatalf("restart: %v", err)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("restart callback did not run")
	}
}

func TestRestartAndUpgradeExcludeEachOther(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	s := NewService(testUpdater(t, srv, "argus"), func() {})
	s.restartDelay = time.Hour
	if _, err := dispatch(t, s, context.Background(), api.MethodServerRestart); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatch(t, s, context.Background(), api.MethodServerUpgrade); !errors.Is(err, ErrBusy) {
		t.Fatalf("upgrade after restart: err %v, want ErrBusy", err)
	}
	if _, err := dispatch(t, s, context.Background(), api.MethodServerRestart); !errors.Is(err, ErrBusy) {
		t.Fatalf("second restart: err %v, want ErrBusy", err)
	}
}
