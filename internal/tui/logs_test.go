package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
func (logsStubClient) Quarantined() bool               { return false }

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
	m.projects.sidebarHidden = true // full-width logs; no tree column
	m.width, m.height = 80, 30      // avail = 26
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
	m.projects.sidebarHidden = true // full-width logs; no tree column
	m.width, m.height = 80, 30      // framed: avail = 24, bottom offset = 76
	m.mode = modeLogs

	mm, _ := m.actLogsUp(tea.KeyPressMsg{})
	m2 := mm.(model)
	if m2.logsFollow {
		t.Fatal("scrolling up should pause follow")
	}
	if m2.logsScroll != 75 { // bottom(76) then up one
		t.Fatalf("logsScroll = %d, want 75", m2.logsScroll)
	}

	// New lines arrive while paused; the pinned top must not move.
	fillLogs(b, 10)
	out := m2.logsView()
	if !strings.Contains(out, "line 75") {
		t.Errorf("paused view should stay pinned at line 75; got:\n%s", out)
	}
}

func TestLogsAreFullBleedWithSidebarHidden(t *testing.T) {
	b := logbuf.New(100)
	fmt.Fprintln(b, strings.Repeat("x", 300))
	m := newModel(logsStubClient{}, false, b)
	m.width, m.height = 80, 20
	m.mode = modeLogs
	m.projects.sidebarHidden = true
	if m.bodyWidth() != m.width {
		t.Fatalf("logs bodyWidth = %d, want the full width %d", m.bodyWidth(), m.width)
	}
	full := strings.Repeat("x", m.width)
	for _, ln := range strings.Split(m.View().Content, "\n") {
		if ln == full {
			return
		}
	}
	t.Errorf("no log line spans the full width from column 0:\n%s", m.View().Content)
}

func TestFullBleedLogsKeepTabsInMargin(t *testing.T) {
	b := logbuf.New(100)
	fmt.Fprintln(b, strings.Repeat("x", 300))
	m := newModel(logsStubClient{}, false, b)
	m.width, m.height = 80, 20
	m.mode = modeLogs
	m.projects.sidebarHidden = true
	for _, ln := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.Contains(ln, "Sessions") {
			if !strings.HasPrefix(ln, strings.Repeat(" ", screenMargin)) || strings.HasPrefix(ln, strings.Repeat(" ", screenMargin+1)) {
				t.Errorf("tabs row should sit exactly in the margin, got %q", ln)
			}
			return
		}
	}
	t.Error("tabs row not found")
}
