package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/logbuf"
)

// logsStubClient is an inert Client for view tests (no events, no calls).
type logsStubClient struct{}

func (logsStubClient) Call(string, any, any) error     { return nil }
func (logsStubClient) Events() <-chan api.Notification { return make(chan api.Notification) }
func (logsStubClient) States() <-chan bool             { return make(chan bool) }
func (logsStubClient) Reconnect()                      {}
func (logsStubClient) Close() error                    { return nil }

func fillLogs(b *logbuf.Buffer, n int) {
	for i := 0; i < n; i++ {
		fmt.Fprintf(b, "line %d\n", i)
	}
}

func TestLogsTabHiddenWithoutBuffer(t *testing.T) {
	m := newModel(logsStubClient{}, false, nil)
	if strings.Contains(m.homeTabs(modeList), "Logs") {
		t.Error("Logs tab should be hidden when no buffer is present")
	}
}

func TestLogsTabShownWithBuffer(t *testing.T) {
	m := newModel(logsStubClient{}, false, logbuf.New(10))
	if !strings.Contains(m.homeTabs(modeList), "Logs") {
		t.Error("Logs tab should be shown when a buffer is present")
	}
}

func TestLogsFollowShowsNewest(t *testing.T) {
	b := logbuf.New(1000)
	fillLogs(b, 100)
	m := newModel(logsStubClient{}, false, b)
	m.width, m.height = 80, 30 // avail = 26
	m.mode = modeLogs
	out := m.logsView()
	if !strings.Contains(out, "line 99") {
		t.Errorf("following view should show newest line; got:\n%s", out)
	}
	if strings.Contains(out, "line 0\n") {
		t.Errorf("following view should not show the oldest line; got:\n%s", out)
	}
}

func TestLogsScrollUpPausesFollowAndPins(t *testing.T) {
	b := logbuf.New(1000)
	fillLogs(b, 100)
	m := newModel(logsStubClient{}, false, b)
	m.width, m.height = 80, 30 // avail = 26, bottom offset = 74
	m.mode = modeLogs

	mm, _ := m.actLogsUp(tea.KeyPressMsg{})
	m2 := mm.(model)
	if m2.logsFollow {
		t.Fatal("scrolling up should pause follow")
	}
	if m2.logsScroll != 73 { // bottom(74) then up one
		t.Fatalf("logsScroll = %d, want 73", m2.logsScroll)
	}

	// New lines arrive while paused; the pinned top must not move.
	fillLogs(b, 10)
	out := m2.logsView()
	if !strings.Contains(out, "line 73") {
		t.Errorf("paused view should stay pinned at line 73; got:\n%s", out)
	}
}
