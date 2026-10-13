package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// refreshLists are the session lists that gr refreshes. The project tree has
// a refreshing hint of its own, so each list draws alone.
var refreshLists = []struct {
	name string
	open func() model
	draw func(m model) string
}{
	{"home", homeTestModel, drawMain},
	{"workspace", func() model { return workspacePane(homeTestModel()) }, drawMain},
	{"sidebar", func() model { return *sidebarSessionsCtx().m }, func(m model) string {
		if m.left.tab != leftSessions {
			return ""
		}
		return m.left.view(&ctx{m: &m}, 40, 20)
	}},
}

func drawMain(m model) string { return m.main[0].view(&ctx{m: &m}, 100, 20) }

// finish feeds the messages of cmd back to m, as the runtime does.
func finish(m model, cmd tea.Cmd) model {
	for _, msg := range execCmd(cmd) {
		m, _ = upd(m, msg)
	}
	return m
}

func TestSessionListsShowRefreshInFlight(t *testing.T) {
	for _, l := range refreshLists {
		t.Run(l.name, func(t *testing.T) {
			showsRefresh := func(m model) bool { return strings.Contains(ansi.Strip(l.draw(m)), "refreshing…") }
			m := l.open()
			m.client = &recordingClient{}
			m, first := upd(m, seqKey("gr"))
			if !showsRefresh(m) {
				t.Fatal("gr should show a refresh in flight")
			}
			m, second := upd(m, seqKey("gr"))
			if m = finish(m, first); !showsRefresh(m) {
				t.Fatal("the hint should stay while a second refresh is in flight")
			}
			if m = finish(m, second); showsRefresh(m) {
				t.Error("the hint should go once every refresh returns")
			}
		})
	}
}

func TestSessionListsReconnectShowsNoRefresh(t *testing.T) {
	for _, l := range refreshLists {
		t.Run(l.name, func(t *testing.T) {
			showsRefresh := func(m model) bool { return strings.Contains(ansi.Strip(l.draw(m)), "refreshing…") }
			rc := &reconnectClient{}
			m := l.open()
			m.client = rc
			m.reconnecting = true
			m, _ = upd(m, seqKey("gr"))
			if rc.reconnects != 1 || showsRefresh(m) {
				t.Errorf("gr while reconnecting: reconnects=%d hint=%v, want a reconnect and no hint", rc.reconnects, showsRefresh(m))
			}
		})
	}
}
