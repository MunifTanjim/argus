package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestLeftTabKeysCycle(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	m = typeKeys(m, "gt")
	if m.left.tab != leftSessions {
		t.Fatalf("gt: tab = %v, want sessions", m.left.tab)
	}
	if _, ok := m.focusedComp().(sidebarSessionsComp); !ok {
		t.Fatalf("focused component = %T, want sidebarSessionsComp", m.focusedComp())
	}
	m = typeKeys(m, "gt")
	if m.left.tab != leftProjects {
		t.Fatalf("gt wraps: tab = %v", m.left.tab)
	}
	m = typeKeys(m, "gT")
	if m.left.tab != leftSessions {
		t.Fatalf("gT: tab = %v", m.left.tab)
	}
}

func TestLeftTabSaved(t *testing.T) {
	m := projectsTestModel()
	m.statePath = filepath.Join(t.TempDir(), "tui.json")
	m = typeKeys(m, "gt")
	if tab, err := loadLeftTab(m.statePath); err != nil || tab != leftSessions {
		t.Fatalf("saved tab = %v, %v; want sessions", tab, err)
	}
}

func TestLeftTabKeysIgnoredWhileTreeRaw(t *testing.T) {
	m := projectsTestModel()
	m = typeKeys(m, "/")
	if !m.inputActive() {
		t.Fatal("/ opens the tree filter")
	}
	m = typeKeys(m, "gt")
	if m.left.tab != leftProjects || m.left.tree.input.Value() != "gt" {
		t.Fatalf("gt in the filter: tab=%v filter=%q", m.left.tab, m.left.tree.input.Value())
	}
}

func TestFocusTreeKeepsSessionsTab(t *testing.T) {
	m := projectsTestModel()
	m.left.tab = leftSessions
	m = withFocus(m, mainPane)
	m.focusTree()
	if m.focused != leftSidebar || m.left.tab != leftSessions {
		t.Fatalf("focused=%v tab=%v; want the sidebar on Sessions", m.focused, m.left.tab)
	}
}

func TestLeftTabClick(t *testing.T) {
	m := withMouse(projectsTestModel())
	m.width, m.height = 160, 30
	// The strip starts at the margin on the sidebar's first row (screen row 2).
	x := screenMargin + len("Projects  ") + 1
	m, _ = click(m, x, 2)
	if m.left.tab != leftSessions {
		t.Fatalf("click on Sessions: tab = %v", m.left.tab)
	}
}

func TestLeftSidebarStrip(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	// The sidebar's strip is on screen row 2, left of the main pane.
	if len(lines) < 3 || !strings.HasPrefix(strings.TrimLeft(lines[2], " │"), "Projects  Sessions") {
		t.Fatalf("sidebar's first row lacks the tab strip:\n%s", strings.Join(lines, "\n"))
	}
}

func TestLeftTabKeysSections(t *testing.T) {
	for _, s := range []string{"project-tree", "sidebar-sessions"} {
		if !offersKey(sectionLists[s].container, projectsKeys.SideTabNext) {
			t.Errorf("%s lacks gt in its container keys", s)
		}
	}
	// The workspace pane takes leftSidebarKeys, and gt is its own tab key there.
	if offersKey(bindingsOf(leftSidebarKeys...), projectsKeys.SideTabNext) {
		t.Error("the workspace pane must keep gt for its own tabs")
	}
}

func TestPaletteScopeIgnoresTreeOnSessionsTab(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.cursor = 2
	if m.paletteScope() == "" {
		t.Fatal("the Projects tab scopes the palette to the tree's cursor row")
	}
	m.left.tab = leftSessions
	if got := m.paletteScope(); got != "" {
		t.Fatalf("Sessions tab: scope = %q, want the main pane's place", got)
	}
}

func TestLeftSessionsTabKeys(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	m.sessions = sidebarSessionsCtx().m.sessions
	m = typeKeys(m, "gt")
	ctrl := func(r rune) func(model) model {
		return func(m model) model { m, _ = upd(m, tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}); return m }
	}
	typed := func(s string) func(model) model { return func(m model) model { return typeKeys(m, s) } }
	for _, s := range []struct {
		name, want string
		press      func(model) model
	}{
		{"j", "n1:b", typed("j")},
		{"k", "n1:a", typed("k")},
		{"G", "n1:c", typed("G")},
		{"gg", "n1:a", typed("gg")},
		{"C-d", "n1:c", ctrl('d')},
		{"C-u", "n1:a", ctrl('u')},
	} {
		if m = s.press(m); m.left.sessions.sel != s.want {
			t.Fatalf("%s: sel = %q, want %q", s.name, m.left.sessions.sel, s.want)
		}
	}
	m, _ = upd(m, keyMsg("esc"))
	if m.focused != mainPane {
		t.Fatalf("esc: focused = %v, want the main pane", m.focused)
	}
	m.focusTree()
	m, _ = upd(m, keyMsg("enter"))
	if tr, ok := m.baseComp().(transcriptComp); !ok || tr.sessionID != "n1:a" {
		t.Fatalf("enter: base = %T, want n1:a's transcript", m.baseComp())
	}
}
