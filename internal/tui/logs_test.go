package tui

import (
	"fmt"
	"strings"
	"testing"

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
	if strings.Contains(m.homeTabs(tabSessions), "Logs") {
		t.Error("Logs tab should be hidden when no buffer is present")
	}
}

func TestLogsTabShownWithBuffer(t *testing.T) {
	m := newModel(logsStubClient{}, false, logbuf.New(10))
	if !strings.Contains(m.homeTabs(tabSessions), "Logs") {
		t.Error("Logs tab should be shown when a buffer is present")
	}
}

func TestLogsFollowShowsNewest(t *testing.T) {
	b := logbuf.New(1000)
	fillLogs(b, 100)
	m := newModel(logsStubClient{}, false, b)
	m.left.hidden = true       // full-width logs; no tree column
	m.width, m.height = 80, 30 // avail = 26
	m = withView(m, viewLogs)
	out := mainView(m)
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
	m.left.hidden = true       // full-width logs; no tree column
	m.width, m.height = 80, 30 // framed: avail = 24, bottom offset = 76
	m = withView(m, viewLogs)

	mm, _ := m.baseKey(keyMsg("k"))
	m2 := mm.(model)
	if logsOf(m2).follow {
		t.Fatal("scrolling up should pause follow")
	}
	if logsOf(m2).scroll != 75 { // bottom(76) then up one
		t.Fatalf("scroll = %d, want 75", logsOf(m2).scroll)
	}

	// New lines arrive while paused; the pinned top must not move.
	fillLogs(b, 10)
	out := mainView(m2)
	if !strings.Contains(out, "line 75") {
		t.Errorf("paused view should stay pinned at line 75; got:\n%s", out)
	}
}

func TestLogsAreFullBleedWithSidebarHidden(t *testing.T) {
	b := logbuf.New(100)
	fmt.Fprintln(b, strings.Repeat("x", 300))
	m := newModel(logsStubClient{}, false, b)
	m.width, m.height = 80, 20
	m = withView(m, viewLogs)
	m.left.hidden = true
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
	m = withView(m, viewLogs)
	m.left.hidden = true
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

func TestHomeTabsCycle(t *testing.T) {
	isHome := func(c component) bool { _, ok := c.(homeComp); return ok }
	isHistory := func(c component) bool { _, ok := c.(historyComp); return ok }
	isLogs := func(c component) bool { _, ok := c.(logsComp); return ok }
	pane := homeTestModel()
	pane.logs = logbuf.New(10)
	fromTree := pressKeys(pane, cw('h')...)
	if viewOf(fromTree) != viewHome || !fromTree.treeFocused() || fromTree.left.tree.cursorRowID() != homeRowID {
		t.Fatalf("<C-w>h should focus the tree on Home: view=%v focus=%v", viewOf(fromTree), fromTree.focused)
	}
	fromTree, _ = upd(fromTree, keyMsg("enter"))
	for name, start := range map[string]model{"pane": pane, "tree": fromTree} {
		if viewOf(start) != viewHome || start.focused != mainPane {
			t.Fatalf("%s: want Home with the pane focused: view=%v focus=%v", name, viewOf(start), start.focused)
		}
		for keys, want := range map[string][]func(component) bool{
			"gt": {isHistory, isLogs, isHome},
			"gT": {isLogs, isHistory, isHome},
		} {
			m := start
			for i, is := range want {
				m = typeKeys(m, keys)
				if len(m.main) != 1 || !is(m.main.top()) || m.focused != mainPane {
					t.Errorf("%s, %s #%d: stack=%#v focus=%v", name, keys, i+1, m.main, m.focused)
				}
			}
		}
	}
}

func TestActiveHomeTabFollowsFocus(t *testing.T) {
	m := withFocus(homeTestModel(), mainPane)
	if tabs := m.homeTabs(tabSessions); !strings.Contains(tabs, StyleAccentBold.Render("Sessions")) {
		t.Errorf("the active tab should use the focus color when the pane has focus: %q", tabs)
	}
	m = withFocus(m, leftSidebar)
	if tabs := m.homeTabs(tabSessions); strings.Contains(tabs, StyleAccentBold.Render("Sessions")) {
		t.Errorf("the active tab should not use the focus color without focus: %q", tabs)
	}
}
