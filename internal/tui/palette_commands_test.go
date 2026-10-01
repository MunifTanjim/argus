package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func treeFocus() model { return withFocus(selectRow(paletteTestModel(), "n1:w1"), leftSidebar) }

func paneFocus() model { return withFocus(selectRow(paletteTestModel(), "n1:w1"), mainPane) }

func commandIDs(m model) []string {
	var out []string
	for _, b := range m.commandSet() {
		out = append(out, "command:"+b.name)
	}
	slices.Sort(out)
	return out
}

func TestPaletteCommandModeListsTheFocusedSection(t *testing.T) {
	for name, m := range map[string]model{"tree": treeFocus(), "workspace pane": paneFocus()} {
		want := commandIDs(m)
		p, _ := paletteOf(typeKeys(openPaletteIn(m), ">"))
		if p.modes[p.mode].title != "Commands" {
			t.Fatalf("%s: mode = %q, want Commands", name, p.modes[p.mode].title)
		}
		if got := matchIDs(p.matches); !slices.Equal(got, want) {
			t.Errorf("%s: commands = %v, want %v", name, got, want)
		}
	}
	tree, pane := commandIDs(treeFocus()), commandIDs(paneFocus())
	if slices.Contains(tree, "command:toggle active-only") || !slices.Contains(pane, "command:toggle active-only") {
		t.Error("the tree and the workspace pane must offer different commands")
	}
}

func TestPaletteCommandShowsItsKeys(t *testing.T) {
	m := typeKeys(openPaletteIn(treeFocus()), ">")
	it, ok := itemByID(func() []paletteItem {
		p, _ := paletteOf(m)
		return p.snap().items
	}(), "command:toggle show-hidden")
	if !ok || it.hint != "z." {
		t.Errorf("toggle show-hidden hint = %q, want \"z.\"", it.hint)
	}
}

func TestPaletteCommandRuns(t *testing.T) {
	m := paletteSelect(t, typeKeys(openPaletteIn(treeFocus()), ">"), "command:toggle show-hidden")
	m, _ = upd(m, keyMsg("enter"))
	if _, ok := paletteOf(m); ok {
		t.Fatal("the palette is still open")
	}
	if !m.left.tree.showHidden {
		t.Error("the command did not run")
	}
}

func TestPaletteBackspaceLeavesCommandMode(t *testing.T) {
	m := typeKeys(openPaletteIn(treeFocus()), ">")
	m, _ = upd(m, backspaceKey)
	p, _ := paletteOf(m)
	if p.modes[p.mode].prefix != "" || p.scope != "ws:n1:w1" {
		t.Errorf("mode %q scope %q, want the default mode and ws:n1:w1", p.modes[p.mode].prefix, p.scope)
	}
}

func TestPaletteCommandModeHasNoScope(t *testing.T) {
	m := typeKeys(openPaletteIn(treeFocus()), ">")
	frame := ansi.Strip(m.View().Content)
	if strings.Contains(frame, "widen") {
		t.Error("command mode must not offer to widen")
	}
	if !strings.Contains(frame, "enter run") {
		t.Error("the footer must name the command's action: enter run")
	}
	assertGolden(t, "palette-commands", typeKeys(m, "toggle"))
}
