package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// fakePopup records the keys and messages it gets. It draws view centered in
// the main pane, or at rect when rect is set.
type fakePopup struct {
	name    string
	section string
	view    string
	rect    *uv.Rectangle
	spin    bool
	closeOn string
	keys    []string
	msgs    int
}

func (p fakePopup) id() string         { return p.name }
func (p fakePopup) keySection() string { return p.section }
func (p fakePopup) spins(*ctx) bool    { return p.spin }

func (p fakePopup) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	p.keys = append(p.keys, msg.String())
	if msg.String() == p.closeOn {
		c.closePopup()
	}
	return p, nil
}

func (p fakePopup) update(*ctx, tea.Msg) (popup, tea.Cmd) {
	p.msgs++
	return p, nil
}

func (p fakePopup) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	if p.rect != nil {
		uv.NewStyledString(p.view).Draw(scr, *p.rect)
		return
	}
	drawCenter(scr, c.m.mainRect(), p.view)
}

func withPopup(m model, p popup) model {
	m.popups = m.popups.open(p)
	return m
}

func frontFake(m model) fakePopup {
	p, _ := m.popups.front().(fakePopup)
	return p
}

func frameLines(m model) []string { return strings.Split(ansi.Strip(m.View().Content), "\n") }

func findBlock(t *testing.T, m model, s string) (row, col int) {
	t.Helper()
	for y, line := range frameLines(m) {
		if i := strings.Index(line, s); i >= 0 {
			return y, ansi.StringWidth(line[:i])
		}
	}
	t.Fatalf("%q not in the view", s)
	return 0, 0
}

func TestPopupStack(t *testing.T) {
	var s popupStack
	if s.front() != nil {
		t.Fatal("an empty stack has a front popup")
	}
	s = s.closeFront()
	s = s.open(fakePopup{name: "a"}).open(fakePopup{name: "b"})
	if len(s) != 2 || s.front().id() != "b" {
		t.Fatalf("stack = %v", s)
	}
	if s = s.replaceFront(fakePopup{name: "c"}); len(s) != 2 || s.front().id() != "c" {
		t.Fatalf("replaceFront = %v", s)
	}
	if s = s.closeFront(); len(s) != 1 || s.front().id() != "a" {
		t.Fatalf("closeFront = %v", s)
	}
	if s = s.closeFront(); len(s) != 0 {
		t.Fatalf("closeFront = %v", s)
	}
}

func TestPopupTakesEveryKey(t *testing.T) {
	m := wideWorkspace()
	cursor, focus := m.left.tree.cursor, m.focused
	m = withPopup(m, fakePopup{name: "f"})
	m = pressKeys(m, keyMsg("j"), keyMsg(":"), ctrlKey('w'), keyMsg("h"))
	if got := strings.Join(frontFake(m).keys, " "); got != "j : ctrl+w h" {
		t.Errorf("popup keys = %q", got)
	}
	if m.left.tree.cursor != cursor || m.focused != focus {
		t.Errorf("a key reached the screen under the popup: cursor %d→%d focus %v→%v", cursor, m.left.tree.cursor, focus, m.focused)
	}
}

func TestPopupCtrlCStillQuits(t *testing.T) {
	if _, cmd := upd(withPopup(wideWorkspace(), fakePopup{name: "f"}), ctrlKey('c')); !quits(cmd) {
		t.Error("ctrl+c must quit while a popup is open")
	}
}

func TestPopupClosesWithoutMovingFocus(t *testing.T) {
	m := wideWorkspace()
	focus := m.focused
	m = pressKeys(withPopup(m, fakePopup{name: "f", closeOn: "esc"}), keyMsg("esc"))
	if len(m.popups) != 0 || m.focused != focus {
		t.Errorf("after esc: popups %d focus %v, want 0 and %v", len(m.popups), m.focused, focus)
	}
}

func TestKeyAfterPopupClosesReachesTheTree(t *testing.T) {
	m := wideWorkspace()
	cursor := m.left.tree.cursor
	m = pressKeys(withPopup(m, fakePopup{name: "f", closeOn: "esc"}), keyMsg("esc"), keyMsg("j"))
	if m.left.tree.cursor == cursor {
		t.Error("j after the popup closed did not move the tree cursor")
	}
}

func TestPopupGetsReplies(t *testing.T) {
	m := withPopup(withPopup(wideWorkspace(), fakePopup{name: "under"}), fakePopup{name: "front"})
	mm, _ := m.updatePopup(branchesMsg{})
	m = mm.(model)
	if frontFake(m).msgs != 1 || m.popups[0].(fakePopup).msgs != 0 {
		t.Errorf("front msgs %d, under msgs %d; want 1 and 0", frontFake(m).msgs, m.popups[0].(fakePopup).msgs)
	}
}

func TestPopupKeySectionIsTheScreen(t *testing.T) {
	if got := withPopup(wideWorkspace(), fakePopup{name: "f", section: "project-tree"}).screen(); got != "project-tree" {
		t.Errorf("screen = %q", got)
	}
	if got := withPopup(wideWorkspace(), fakePopup{name: "f"}).screen(); got != "" {
		t.Errorf("screen with no key section = %q", got)
	}
}

func TestPopupCenteredInMainPane(t *testing.T) {
	treeHidden := withFocus(wideWorkspace(), mainPane)
	treeHidden.left.hidden = true
	treeHidden, _ = treeHidden.syncSidebar()
	for name, m := range map[string]model{"tree": wideWorkspace(), "tree hidden": treeHidden} {
		m = withPopup(m, fakePopup{name: "f", view: "@@@@@@\n@@@@@@"})
		row, col := findBlock(t, m, "@@@@@@")
		r := m.mainRect()
		if want := r.Min.X + r.Dx()/2 - 3; col != want {
			t.Errorf("%s: popup at column %d, want %d (main pane %v)", name, col, want, r)
		}
		if row < r.Min.Y || row+2 > r.Max.Y {
			t.Errorf("%s: popup at row %d, outside the main pane %v", name, row, r)
		}
	}
}

func TestPopupStaysInMainPaneAfterResize(t *testing.T) {
	m := withPopup(wideWorkspace(), fakePopup{name: "f", view: "@@@@@@"})
	m, _ = upd(m, tea.WindowSizeMsg{Width: 70, Height: 16})
	_, col := findBlock(t, m, "@@@@@@")
	if r := m.mainRect(); col < r.Min.X || col+6 > r.Max.X {
		t.Errorf("popup at column %d, outside the main pane %v after resize", col, r)
	}
}

func TestPopupInTinyTerminal(t *testing.T) {
	m := withPopup(wideWorkspace(), fakePopup{name: "f", view: strings.Repeat(strings.Repeat("@", 60)+"\n", 20)})
	m, _ = upd(m, tea.WindowSizeMsg{Width: 30, Height: 8})
	for i, line := range frameLines(m) {
		if w := ansi.StringWidth(line); w > 30 {
			t.Errorf("line %d is %d wide in a 30-column terminal", i, w)
		}
	}
}

// A popup edge on either cell of a wide rune keeps the row's width and leaves
// the text right of the popup in place.
func TestPopupOverWideRunesKeepsLineWidth(t *testing.T) {
	m := wideWorkspace()
	m.left.tree.data[0].Name = "日本語日本語"
	m.left.tree.rebuild()
	row, col := findBlock(t, m, "日本語")
	before := strings.TrimRight(frameLines(m)[row], " ")
	right := before[strings.Index(before, "│"):]
	for _, x := range []int{col, col + 1} {
		r := uv.Rect(x, row, 1, 1)
		got := strings.TrimRight(frameLines(withPopup(m, fakePopup{name: "f", view: "@", rect: &r}))[row], " ")
		if ansi.StringWidth(got) != ansi.StringWidth(before) || !strings.HasSuffix(got, right) {
			t.Errorf("popup at column %d:\n got %q\nwant the width and the tail of %q", x, got, before)
		}
	}
}

func TestDrawCenterClampsToTheArea(t *testing.T) {
	scr := uv.NewScreenBuffer(10, 4)
	drawCenter(scr, uv.Rect(2, 1, 4, 2), "abcdefgh\nabcdefgh\nabcdefgh")
	lines := strings.Split(ansi.Strip(scr.Render()), "\n")
	got := make([]string, len(lines))
	for i, l := range lines {
		got[i] = strings.TrimRight(l, " ")
	}
	if strings.Join(got, "|") != "|  abcd|  abcd|" {
		t.Errorf("drawn = %q", got)
	}
}

func TestPopupFooterShowsOnlyTheFlash(t *testing.T) {
	m := withPopup(wideWorkspace(), fakePopup{name: "f"})
	lines := frameLines(m)
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		t.Errorf("footer under a popup = %q, want empty", last)
	}
	m.flash = "saved"
	lines = frameLines(m)
	if !strings.Contains(lines[len(lines)-1], "saved") {
		t.Errorf("footer under a popup lost the flash: %q", lines[len(lines)-1])
	}
}

func TestPopupSpinsTheFrame(t *testing.T) {
	if !withPopup(wideWorkspace(), fakePopup{name: "f", spin: true}).spinShown() {
		t.Error("a spinning popup does not arm the spinner")
	}
}

func TestTreeDrawsUnfocusedUnderPopup(t *testing.T) {
	m := wideWorkspace()
	want := treePane(withFocus(m, mainPane), 30, 20)
	if got := treePane(withPopup(m, fakePopup{name: "f"}), 30, 20); got != want {
		t.Errorf("tree under a popup:\n got: %q\nwant: %q", got, want)
	}
}

func TestPopupFrameFitsItsWidth(t *testing.T) {
	f := popupFrame{width: 20, height: 8, title: "A very long popup title", titleInfo: "info", parts: []string{"line one that is too long for the box"}, help: "enter ok · esc cancel"}
	out := ansi.Strip(f.render())
	lines := strings.Split(out, "\n")
	if len(lines) != 8 {
		t.Fatalf("frame has %d rows, want 8:\n%s", len(lines), out)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 20 {
			t.Errorf("row %d is %d wide, want 20: %q", i, w, l)
		}
	}
	if !strings.Contains(lines[1], "A very long pop…") || strings.Contains(lines[1], "info") {
		t.Errorf("title row = %q: want the title truncated and the info dropped", lines[1])
	}
	if f.innerWidth() != 16 || f.bodyHeight() != 4 || strings.TrimSpace(strings.Trim(lines[2], "│")) == "" {
		t.Errorf("inner %d body %d row 2 %q: want 16, 4, and no blank row in a short frame", f.innerWidth(), f.bodyHeight(), lines[2])
	}
	f.height = 10
	if lines := strings.Split(ansi.Strip(f.render()), "\n"); f.bodyHeight() != 4 || strings.TrimSpace(strings.Trim(lines[2], "│")) != "" {
		t.Errorf("body %d row 2 %q: want 4 and a blank row under the title", f.bodyHeight(), lines[2])
	}
}

func TestPasteGoesToThePopup(t *testing.T) {
	m, _ := upd(withPopup(wideWorkspace(), fakePopup{name: "f"}), tea.PasteMsg{Content: "x"})
	if frontFake(m).msgs != 1 {
		t.Errorf("the popup got %d messages from a paste, want 1", frontFake(m).msgs)
	}
}

func TestLiveScreenTakesKeysBeforeAPopup(t *testing.T) {
	m := pressKeys(withPopup(liveScreenModel(), fakePopup{name: "f"}), keyMsg("j"))
	if keys := frontFake(m).keys; len(keys) != 0 {
		t.Errorf("the popup got %v over the live screen", keys)
	}
}
