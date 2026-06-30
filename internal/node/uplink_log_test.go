package node

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"

	"net/http/httptest"
)

// syncBuffer is a goroutine-safe io.Writer for capturing logs written from the
// uplink/serve goroutines while the test reads them.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func debugLogger(w *syncBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestUplinkLogsEstablished(t *testing.T) {
	agg := gateway.New(time.Second)
	hsrv := gateway.NewServer(agg,
		func(tok string) bool { return tok == "dtok" },
		func(tok string) bool { return tok == "ctok" },
	)
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetIdentity("home", "home-box")
	var logs syncBuffer
	d.SetLogger(debugLogger(&logs))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.ConnectGateway(ctx, wsURL(ts.URL)+"/node", "dtok", nil)

	waitFor(t, func() bool { return strings.Contains(logs.String(), "gateway uplink established") })
	if strings.Contains(logs.String(), "dial failed") {
		t.Errorf("a successful uplink should not log a dial failure:\n%s", logs.String())
	}
}

func TestUplinkLogsDialFailure(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	var logs syncBuffer
	d.SetLogger(debugLogger(&logs))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Nothing listens on this port: the dial fails fast and should be logged.
	go d.ConnectGateway(ctx, "ws://127.0.0.1:1/node", "dtok", nil)

	waitFor(t, func() bool { return strings.Contains(logs.String(), "gateway uplink dial failed") })
}

func TestRunLogsServingLocalAPI(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	var logs syncBuffer
	d.SetLogger(debugLogger(&logs))

	sock := filepath.Join(t.TempDir(), "argus.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx, sock) }()

	waitFor(t, func() bool { return strings.Contains(logs.String(), "serving local API") })
}
