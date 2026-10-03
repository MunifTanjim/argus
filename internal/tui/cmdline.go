package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

// cmdMark starts the text of the synthetic key press that runs a command by
// name. Like seqMark, it is a private-use rune that no real key press has.
const cmdMark = "\U000F0001"

func cmdMsg(name string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyExtended, Text: cmdMark + name}
}

// commandSet is the commands that act on the focused component now: its own,
// its container's, the focus manager's moves that go somewhere, and the help
// and quit it offers.
func (m model) commandSet() []binding {
	l := sectionLists[m.screen()]
	if len(l.focus) == 0 {
		return nil
	}
	var out []binding
	seen := map[string]bool{}
	add := func(bs ...binding) {
		for _, b := range bs {
			if b.name == "" || seen[b.name] || textInputOnly(b.name) || !m.applies(b) {
				continue
			}
			seen[b.name] = true
			out = append(out, b)
		}
	}
	add(m.focusedComp().commands(&ctx{m: &m})...)
	add(l.container...)
	add(m.focusCommands()...)
	for _, b := range []binding{projectsKeys.Help, listKeys.Quit} {
		if m.offered(b) {
			add(b)
		}
	}
	return out
}

// applies reports whether b acts now. Of each pair that shares a key, only
// the half that fits the selected project applies.
func (m model) applies(b binding) bool {
	k := projectsKeys
	switch b.name {
	case nodeKeys.Wakelock.name:
		r, ok := m.left.tree.cursorRow()
		return ok && r.kind == rowNode
	case k.Pin.name, k.Unpin.name, k.Hide.name, k.Unhide.name:
	default:
		return true
	}
	id := m.left.tree.cursorProjectID()
	if id == "" {
		return false
	}
	p, _ := m.left.tree.findProject(id)
	switch b.name {
	case k.Pin.name:
		return !p.Pinned
	case k.Unpin.name:
		return p.Pinned
	case k.Hide.name:
		return !p.Hidden
	}
	return p.Hidden
}

// applying is the half of a shared-key pair that applies now, or a when
// neither does.
func (m model) applying(a, b binding) binding {
	if m.applies(b) {
		return b
	}
	return a
}

var cmdAliases = map[string]string{"q": "quit", "h": "help"}

const cmdListRows = 10

// cmdMatch is a command that matches the typed text, and the byte positions
// of the matched characters in its name.
type cmdMatch struct {
	binding
	hits []int
}

type commandNames []binding

func (s commandNames) String(i int) string { return s[i].name }
func (s commandNames) Len() int            { return len(s) }

// matchCommands fuzzy-matches set against typed, best match first, the way
// crush filters its completions. Equal scores keep name order.
func matchCommands(set []binding, typed string) []cmdMatch {
	names := slices.SortedFunc(slices.Values(set), func(a, b binding) int { return strings.Compare(a.name, b.name) })
	q := strings.ToLower(strings.TrimSpace(typed))
	if q == "" {
		out := make([]cmdMatch, len(names))
		for i, b := range names {
			out[i] = cmdMatch{binding: b}
		}
		return out
	}
	found := fuzzy.FindFrom(q, commandNames(names))
	out := make([]cmdMatch, len(found))
	for i, f := range found {
		out[i] = cmdMatch{binding: names[f.Index], hits: f.MatchedIndexes}
	}
	return out
}

func cmdMatchStyle(base lipgloss.Style) lipgloss.Style { return base.Bold(true).Underline(true) }

// Each run is rendered on its own, so that a reset in one run does not drop
// the base style of the next.
func highlightName(name string, hits []int, base lipgloss.Style) string {
	hit := cmdMatchStyle(base)
	var b strings.Builder
	start := 0
	for i := range name {
		if i > start && slices.Contains(hits, i) != slices.Contains(hits, start) {
			b.WriteString(runStyle(hits, start, base, hit).Render(name[start:i]))
			start = i
		}
	}
	if start < len(name) {
		b.WriteString(runStyle(hits, start, base, hit).Render(name[start:]))
	}
	return b.String()
}

func runStyle(hits []int, start int, base, hit lipgloss.Style) lipgloss.Style {
	if slices.Contains(hits, start) {
		return hit
	}
	return base
}

func (m model) opensCmdLine(msg tea.KeyPressMsg) bool {
	return msg.String() == ":" && len(m.keyBuf) == 0 && !m.keysRaw() && len(m.commandSet()) > 0
}

// cmdLinePopup is the open command line. sel is -1 before the first tab;
// histPos is -1 outside a history walk, and histPrefix is the text typed
// before it. screen is the section it opened on, whose keys the list shows.
type cmdLinePopup struct {
	input      textinput.Model
	screen     string
	set        []binding
	matches    []cmdMatch
	sel        int
	histPos    int
	histPrefix string
}

func (cmdLinePopup) id() string           { return "cmdline" }
func (p cmdLinePopup) keySection() string { return p.screen }
func (cmdLinePopup) spins(*ctx) bool      { return false }
func (p cmdLinePopup) update(c *ctx, msg tea.Msg) (popup, tea.Cmd) {
	if msg, ok := msg.(tea.PasteMsg); ok {
		p.input.SetWidth(cmdInputWidth(c.m.width))
		return p.edit(msg)
	}
	return p, nil
}

func (m model) openCmdLine() (tea.Model, tea.Cmd) {
	in := textinput.New()
	in.Prompt = ""
	set := m.commandSet()
	m.flash = ""
	p := cmdLinePopup{input: in, screen: m.screen(), set: set, matches: matchCommands(set, ""), sel: -1, histPos: -1}
	cmd := p.input.Focus()
	m.popups = m.popups.open(p)
	return m, cmd
}

func (p cmdLinePopup) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	// The input scrolls by the width it holds when it updates.
	p.input.SetWidth(cmdInputWidth(c.m.width))
	switch msg.String() {
	case "esc":
		c.closePopup()
		return p, nil
	case "enter":
		c.closePopup()
		c.runCommand(strings.TrimSpace(p.input.Value()))
		return p, nil
	case "backspace":
		if p.input.Value() == "" {
			c.closePopup()
			return p, nil
		}
	case "tab", "shift+tab", "ctrl+n", "ctrl+p":
		p.cycle(msg.String() == "tab" || msg.String() == "ctrl+n")
		return p, nil
	case "up", "down":
		p.walkHistory(c.m.cmdHistory, msg.String() == "up")
		return p, nil
	}
	return p.edit(msg)
}

func (p cmdLinePopup) edit(msg tea.Msg) (popup, tea.Cmd) {
	before := p.input.Value()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != before {
		p.matches, p.sel, p.histPos = matchCommands(p.set, p.input.Value()), -1, -1
	}
	return p, cmd
}

func (p *cmdLinePopup) cycle(forward bool) {
	n := len(p.matches)
	if n == 0 {
		return
	}
	switch {
	case p.sel < 0 && forward:
		p.sel = 0
	case p.sel < 0:
		p.sel = n - 1
	case forward:
		p.sel = (p.sel + 1) % n
	default:
		p.sel = (p.sel - 1 + n) % n
	}
	p.input.SetValue(p.matches[p.sel].name)
	p.input.CursorEnd()
}

// walkHistory steps to the previous (back) or next entry that starts with the
// text typed before the walk; past the newest entry it shows that text again.
func (p *cmdLinePopup) walkHistory(hist []string, back bool) {
	if p.histPos < 0 {
		if !back {
			return
		}
		p.histPrefix, p.histPos = p.input.Value(), len(hist)
	}
	for i := p.histPos; ; {
		if back {
			i--
		} else {
			i++
		}
		switch {
		case i < 0:
			return
		case i >= len(hist):
			p.histPos = -1
			p.show(p.histPrefix)
			return
		case strings.HasPrefix(hist[i], p.histPrefix):
			p.histPos = i
			p.show(hist[i])
			return
		}
	}
}

func (p *cmdLinePopup) show(s string) {
	p.input.SetValue(s)
	p.input.CursorEnd()
	p.matches, p.sel = matchCommands(p.set, s), -1
}

// Focus can move and a prompt can appear while the line is open, so the set
// and the raw state are read again.
func (m model) runCmdLine(typed string) (tea.Model, tea.Cmd) {
	if typed == "" || m.keysRaw() {
		return m, nil
	}
	name := normalizeCommand(typed)
	if full, ok := cmdAliases[name]; ok {
		name = full
	}
	if !slices.ContainsFunc(m.commandSet(), func(b binding) bool { return b.name == name }) {
		m.flash = "unknown command: " + typed
		return m, nil
	}
	if n := len(m.cmdHistory); n == 0 || m.cmdHistory[n-1] != typed {
		m.cmdHistory = append(m.cmdHistory, typed)
	}
	return m.runSequence([]tea.KeyPressMsg{cmdMsg(name)}, nil, nil)
}

func cmdInputWidth(w int) int { return max(1, w-2*screenMargin-1) }

func (p cmdLinePopup) draw(c *ctx, scr uv.Screen, area uv.Rectangle) {
	w := area.Dx()
	p.input.SetWidth(cmdInputWidth(w))
	row := strings.Repeat(" ", screenMargin) + ":" + p.input.View()
	row += strings.Repeat(" ", max(0, w-lipgloss.Width(row)))
	uv.NewStyledString(row).Draw(scr, bottomLeftRect(area, w, 1))
	if len(p.matches) == 0 {
		return
	}
	// The box's border and padding put the matches under the typed text.
	x := screenMargin - 1
	box := p.listBox(c, w-2*x)
	bw, bh := lipgloss.Size(box)
	above := uv.Rect(area.Min.X+x, area.Min.Y, bw, area.Dy()-1)
	uv.NewStyledString(box).Draw(scr, bottomLeftRect(above, bw, bh))
}

func keyLabels(sk *screenKeys, b binding) string {
	var labels []string
	for _, id := range sk.listedKeys(b) {
		labels = append(labels, keyLabel(id))
	}
	return strings.Join(labels, " ")
}

// The width fits every match, not only the shown ones, so it stays while
// scrolling.
func (p cmdLinePopup) listBox(c *ctx, maxW int) string {
	n := max(1, min(cmdListRows, c.m.height-3))
	start := 0
	if p.sel >= n {
		start = p.sel - n + 1
	}
	end := min(len(p.matches), start+n)
	sk := c.m.keymap().screenKeys(p.screen)
	keys := make([]string, len(p.matches))
	nameW, keysW := 0, 0
	for i, b := range p.matches {
		keys[i] = keyLabels(sk, b.binding)
		nameW, keysW = max(nameW, len(b.name)), max(keysW, lipgloss.Width(keys[i]))
	}
	f := popupFrame{width: min(maxW, nameW+2+keysW+popupFrameW)}
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		base := lipgloss.NewStyle()
		if i == p.sel {
			base = StyleSecondary.Reverse(true)
		}
		m := p.matches[i]
		name := highlightName(m.name, m.hits, base) + base.Render(strings.Repeat(" ", nameW-len(m.name)))
		row := name + "  " + StyleDim.Render(fmt.Sprintf("%-*s", keysW, keys[i]))
		rows = append(rows, xansi.Truncate(row, f.innerWidth(), "…"))
	}
	f.parts = rows
	return f.render()
}
