package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// branchPicker is a filterable branch list: the create picker's Branches tab
// and target field, and the T (change target) flow.
type branchPicker struct {
	branches []api.BranchInfo
	loaded   bool
	err      error
	filter   textinput.Model
	cursor   int
	current  string // marked in the list, and where the cursor starts
}

func newBranchPicker() branchPicker {
	f := textinput.New()
	f.Prompt = ""
	f.Focus()
	return branchPicker{filter: f}
}

func (b branchPicker) matches() []api.BranchInfo {
	q := strings.ToLower(strings.TrimSpace(b.filter.Value()))
	var out []api.BranchInfo
	for _, br := range b.branches {
		if q == "" || strings.Contains(strings.ToLower(br.Name), q) {
			out = append(out, br)
		}
	}
	return out
}

func (b *branchPicker) load(branches []api.BranchInfo, err error) {
	b.branches, b.loaded, b.err = branches, true, err
	for i, br := range b.matches() {
		if br.Name == b.current {
			b.cursor = i
		}
	}
}

func (b *branchPicker) key(msg tea.KeyPressMsg) (*api.BranchInfo, tea.Cmd) {
	ms := b.matches()
	switch msg.String() {
	case "up":
		b.cursor = cursorUp(b.cursor)
		return nil, nil
	case "down":
		b.cursor = cursorDown(b.cursor, len(ms))
		return nil, nil
	case "enter":
		if b.cursor < len(ms) {
			return &ms[b.cursor], nil
		}
		return nil, nil
	}
	var cmd tea.Cmd
	b.filter, cmd = b.filter.Update(msg)
	b.cursor = min(b.cursor, cursorBottom(len(b.matches())))
	return nil, cmd
}

func (b *branchPicker) paste(msg tea.PasteMsg) tea.Cmd {
	var cmd tea.Cmd
	b.filter, cmd = b.filter.Update(msg)
	b.cursor = min(b.cursor, cursorBottom(len(b.matches())))
	return cmd
}

// dimInUse marks branches that a worktree already has checked out.
func (b branchPicker) view(c *ctx, w, h int, dimInUse bool) string {
	head := dimStyle.Render("filter: ") + b.filter.View()
	switch {
	case b.err != nil:
		return head + "\n\n" + dimStyle.Render("error: "+firstLine(b.err.Error()))
	case !b.loaded:
		return head + "\n\n" + dimStyle.Render("loading branches…")
	}
	ms := b.matches()
	if len(ms) == 0 {
		return head + "\n\n" + dimStyle.Render("no matches")
	}
	l := itemLines{key: "branch-picker"}
	for i, br := range ms {
		text := br.Name
		if br.Remote && !br.Local {
			text += dimStyle.Render("  origin")
		}
		if dimInUse && br.CheckedOut {
			text = dimStyle.Render(br.Name + "  (in use)")
		}
		if br.Name == b.current {
			text += dimStyle.Render("  (current)")
		}
		l.add(i, truncateLine(cursorLine(text, i == b.cursor, true), w))
	}
	return head + "\n\n" + strings.Join(l.window(c.below(2), b.cursor, max(1, h-2)), "\n")
}
