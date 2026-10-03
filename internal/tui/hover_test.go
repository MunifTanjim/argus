package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func hoverText(m model) string {
	it, ok := m.hoverItem()
	if !ok || m.hovered != it.key {
		return ""
	}
	var b strings.Builder
	b.WriteString(it.title + "\n")
	for _, f := range it.fields {
		b.WriteString(f[0] + ": " + f[1] + "\n")
	}
	return b.String()
}

func assertHover(t *testing.T, m model, want ...string) {
	t.Helper()
	got := hoverText(m)
	if got == "" {
		t.Fatal("the hover is not open")
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the hover misses %q:\n%s", w, got)
		}
	}
}

func TestHoverShowsTreeRows(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.data[0].DefaultBranch = "main"
	m.left.tree.data[0].Error = "fatal: not a git repository (or any of the parent directories)"
	m.left.tree.data[0].Workspaces[1].TargetBranch = "main"
	m.left.tree.rebuild()

	m = typeKeys(moveTree(m, 1), "K")
	assertHover(t, m, "home", "ID: n1")

	m = typeKeys(moveTree(m, 2), "K")
	assertHover(t, m, "argus", "Dir: /repo/.git", "Kind: git", "Default branch: main", "Node: home",
		"Error: fatal: not a git repository (or any of the parent directories)")

	m = typeKeys(moveTree(m, 4), "K")
	assertHover(t, m, "repo-feat", "Dir: /repo-feat", "Branch: feature", "Target: main")
}

func TestHoverIgnoresHomeRow(t *testing.T) {
	m := typeKeys(moveTree(projectsTestModel(), 0), "K")
	if m.hovered != "" {
		t.Errorf("the Home row has no hover, got %q", m.hovered)
	}
}

func TestHoverShowsFileTreeEntry(t *testing.T) {
	m := filesFocused()
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "", entries: []api.DirEntry{
		{Name: "link", Path: "link", Symlink: true, Target: "some/very/long/target/path"},
	}})
	m = typeKeys(m, "K")
	assertHover(t, m, "link", "Path: link", "Type: symlink", "Target: some/very/long/target/path")
}

func TestHoverShowsChangesRows(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "internal/new.go", OrigPath: "internal/old.go", Change: "renamed", Staged: true, Unstaged: true})
	m, _ = upd(m, commitsMsg{ws: "n1:w1", commits: []api.Commit{{SHA: "abc1234def", Short: "abc1234", Subject: "fix: a long subject line", Author: "Ann"}}})
	m = typeKeys(m, "K")
	assertHover(t, m, "new.go", "Path: internal/new.go", "From: internal/old.go", "Change: renamed", "State: staged, unstaged")
	m = typeKeys(m, "jK")
	assertHover(t, m, "abc1234", "SHA: abc1234def", "Subject: fix: a long subject line", "Author: Ann")
}

func TestHoverOpensNextToTheItem(t *testing.T) {
	m := moveTree(projectsTestModel(), 2)
	m.hits = &hitMap{}
	m.height = 40
	m = typeKeys(m, "K")
	_, y := itemCell(t, m, regTree, 2)
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if !strings.HasPrefix(strings.TrimSpace(lines[y+1]), "╭") {
		t.Errorf("the hover must open on the row below the item, line %d = %q", y+1, lines[y+1])
	}

	m = moveTree(m, 4)
	m.height = 12
	m = typeKeys(m, "K")
	_, y = itemCell(t, m, regTree, 4)
	lines = strings.Split(ansi.Strip(m.View().Content), "\n")
	if !strings.HasPrefix(strings.TrimSpace(lines[y-1]), "╰") {
		t.Errorf("with no room below, the hover must end on the row above the item, line %d = %q", y-1, lines[y-1])
	}
}

func TestHoverWrapsLongValues(t *testing.T) {
	m := projectsTestModel()
	m.hits = &hitMap{}
	m.width = 80
	m.left.tree.data[0].Error = strings.Repeat("word ", 30) + "END"
	m.left.tree.rebuild()
	m = typeKeys(moveTree(m, 2), "K")
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "END") {
		t.Errorf("a long value must wrap, not truncate:\n%s", out)
	}
	assertFits(t, out, 80)
}

func TestHoverCloses(t *testing.T) {
	open := func() model { return typeKeys(moveTree(projectsTestModel(), 2), "K") }

	if m := typeKeys(open(), "K"); m.hovered == "" {
		t.Error("K must keep the hover open")
	}
	m, _ := upd(open(), keyMsg("esc"))
	if m.hovered != "" || m.left.tree.cursor != 2 {
		t.Errorf("<Esc> must only close the hover: hover=%q cursor=%d", m.hovered, m.left.tree.cursor)
	}
	m = typeKeys(open(), "j")
	if m.hovered != "" || m.left.tree.cursor != 3 {
		t.Errorf("j must close the hover and move the cursor: hover=%q cursor=%d", m.hovered, m.left.tree.cursor)
	}
	m = withFocus(open(), mainPane)
	m, _ = upd(m, logTickMsg{})
	if m.hovered != "" {
		t.Error("a focus change must close the hover")
	}
	m = open()
	m, _ = m.openPalette()
	m, _ = upd(m, logTickMsg{})
	if m.hovered != "" {
		t.Error("a popup must close the hover")
	}
}
