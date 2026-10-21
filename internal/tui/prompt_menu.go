package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

// promptTriggers are the characters that open the agent's completion menu.
func promptTriggers(agent string) string {
	switch agent {
	case "claude", "antigravity":
		return "/"
	case "codex":
		return "/$"
	case "opencode":
		return "/@"
	}
	return ""
}

// completionQuery finds the trigger and the partial name typed at the end of
// before. "/" counts only as the first character of the prompt; the other
// triggers start any word.
func completionQuery(triggers, before string) (trigger byte, query string, ok bool) {
	if before == "" {
		return 0, "", false
	}
	if before[0] == '/' && strings.IndexByte(triggers, '/') >= 0 && !strings.ContainsFunc(before, unicode.IsSpace) {
		return '/', before[1:], true
	}
	word := before
	if i := strings.LastIndexFunc(before, unicode.IsSpace); i >= 0 {
		_, n := utf8.DecodeRuneInString(before[i:])
		word = before[i+n:]
	}
	if word == "" || word[0] == '/' || strings.IndexByte(triggers, word[0]) < 0 {
		return 0, "", false
	}
	return word[0], word[1:], true
}

// applyCompletion replaces the trailing trigger and query of before with the
// chosen name.
func applyCompletion(before string, trigger byte, query, name string) string {
	return strings.TrimSuffix(before, string(trigger)+query) + string(trigger) + name + " "
}

type promptMatch struct {
	api.PromptCompletion
	hits []int
}

// promptMenu is active while the cursor ends a word that starts with trigger.
// dismissed hides it until the cursor leaves that word.
type promptMenu struct {
	dismissed bool
	trigger   byte
	query     string
	matches   []promptMatch
	sel       int
	nameW     int
	descW     int
}

func (p promptMenu) shown() bool { return p.trigger != 0 && !p.dismissed && len(p.matches) > 0 }

func (p *promptMenu) setMatches(items []api.PromptCompletion) {
	p.matches = matchCompletions(items, p.query)
	p.nameW, p.descW = 0, 0
	for _, it := range p.matches {
		p.nameW, p.descW = max(p.nameW, lipgloss.Width(it.Name)), max(p.descW, lipgloss.Width(firstLine(it.Description)))
	}
}

func matchCompletions(items []api.PromptCompletion, query string) []promptMatch {
	found := fuzzyFind(items, func(it api.PromptCompletion) string { return it.Name }, query)
	out := make([]promptMatch, len(found))
	for i, f := range found {
		out[i] = promptMatch{PromptCompletion: items[f.Index], hits: f.MatchedIndexes}
	}
	return out
}

func (m model) promptCompleteCmd(id string, trigger byte, seq int) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.PromptCompleteResult
		err := client.Call(api.MethodSessionPromptComplete, api.PromptCompleteParams{SessionID: id, Trigger: string(trigger)}, &res)
		return promptCompleteMsg{id: id, trigger: trigger, seq: seq, items: res.Items, err: err}
	}
}

// replyBefore is the reply text up to the cursor.
func (d dockComp) replyBefore() string {
	lines := strings.Split(d.reply.Value(), "\n")
	row := min(d.reply.Line(), len(lines)-1)
	line := []rune(lines[row])
	col := min(d.reply.Column(), len(line))
	return strings.Join(append(lines[:row:row], string(line[:col])), "\n")
}

// syncMenu follows the reply after an edit. Entering a trigger word with no
// cached items asks the agent for them; a later fetch supersedes it.
func (d *dockComp) syncMenu(c *ctx) tea.Cmd {
	id := c.m.liveSessionID()
	triggers := promptTriggers(c.m.sessions[id].Agent)
	if triggers == "" {
		d.menu = promptMenu{}
		return nil
	}
	trigger, query, ok := completionQuery(triggers, d.replyBefore())
	if !ok {
		d.menu = promptMenu{}
		return nil
	}
	if trigger == d.menu.trigger && query == d.menu.query {
		return nil
	}
	fresh := d.menu.trigger != trigger
	if fresh {
		d.menu = promptMenu{trigger: trigger}
	}
	d.menu.sel = 0
	d.menu.query = query
	items, cached := c.m.completions[id][trigger]
	d.menu.setMatches(items)
	if fresh && !cached {
		d.completeSeq++
		return c.m.promptCompleteCmd(id, trigger, d.completeSeq)
	}
	return nil
}

// handleMenuKey takes the menu's keys, the way crush's completions do.
func (d dockComp) handleMenuKey(c *ctx, msg tea.KeyPressMsg) (dockComp, tea.Cmd, bool) {
	n := len(d.menu.matches)
	switch msg.String() {
	case "down", "ctrl+n":
		d.menu.sel = (d.menu.sel + 1) % n
	case "up", "ctrl+p":
		d.menu.sel = (d.menu.sel - 1 + n) % n
	case "enter", "tab":
		d.selectCompletion(c)
	case "esc":
		d.menu.dismissed = true
	default:
		return d, nil, false
	}
	return d, nil, true
}

// selectCompletion keeps the text after the cursor and leaves the cursor
// after the inserted name.
func (d *dockComp) selectCompletion(c *ctx) {
	p := d.menu
	before := d.replyBefore()
	after := d.reply.Value()[len(before):]
	d.reply.SetValue(after)
	d.reply.MoveToBegin()
	d.reply.InsertString(applyCompletion(before, p.trigger, p.query, p.matches[p.sel].Name))
	d.sizeIdleReply(c)
	d.menu = promptMenu{}
}

func (m model) promptMenuShown() bool {
	return m.dockDrawn() && m.dockFocused() && m.inputShown() && m.dock.menu.shown()
}

func (m model) updatePromptComplete(msg promptCompleteMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.dock.completeSeq || msg.id != m.dock.session || msg.err != nil || len(msg.items) == 0 {
		return m, nil
	}
	if m.completions[msg.id] == nil {
		m.completions[msg.id] = map[byte][]api.PromptCompletion{}
	}
	m.completions[msg.id][msg.trigger] = msg.items
	if m.dock.menu.trigger == msg.trigger {
		m.dock.menu.setMatches(msg.items)
	}
	return m, nil
}

// drawPromptMenu draws the menu over the transcript, right above the dock's
// top rule, with its names in the input's text column.
func (m model) drawPromptMenu(scr uv.Screen) {
	l := m.layout()
	cw := containerWidthOf(l.dockW)
	left := l.dockX + centerGutter(cw, l.dockW)
	top := m.mainRect().Min.Y
	ruleY := top + m.sessionRows() - m.dockRows() + 1
	x := left + lipgloss.Width(promptMarker) - popupFrameW/2
	p := m.dock.menu
	n := min(cmdListRows, len(p.matches), ruleY-top-popupFrameH)
	if n < 1 {
		return
	}
	start := max(0, p.sel-n+1)
	f := popupFrame{width: min(left+cw-x, p.nameW+2+p.descW+popupFrameW)}
	descRoom := f.innerWidth() - p.nameW - 2
	for i := start; i < start+n; i++ {
		base, descStyle := lipgloss.NewStyle(), StyleDim
		if i == p.sel {
			base = StyleSecondary.Reverse(true)
			descStyle = base
		}
		it := p.matches[i]
		row := highlightName(it.Name, it.hits, base) + base.Render(strings.Repeat(" ", p.nameW-lipgloss.Width(it.Name)))
		if descRoom > 0 {
			desc := xansi.Truncate(firstLine(it.Description), descRoom, "…")
			row += base.Render("  ") + descStyle.Render(desc+strings.Repeat(" ", descRoom-lipgloss.Width(desc)))
		}
		f.parts = append(f.parts, xansi.Truncate(row, f.innerWidth(), "…"))
	}
	box := f.render()
	bw, bh := lipgloss.Size(box)
	uv.NewStyledString(box).Draw(scr, bottomLeftRect(uv.Rect(x, top, bw, ruleY-top), bw, bh))
}
