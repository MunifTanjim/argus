package tui

import (
	"fmt"
	"image/color"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/transcript"
)

type argusKindStyle struct {
	label    string
	color    color.Color
	markdown bool
}

// argusKinds follows the argus-ui Claude Code plugin: messages draw their body
// as markdown, notices keep their lines (tool input) as written.
var argusKinds = map[string]argusKindStyle{
	"text":              {"message", ColorContextOk, true},
	"task":              {"task", ColorInfo, true},
	"reply":             {"reply", ColorContextOk, true},
	"interaction":       {"prompt", ColorContextWarn, false},
	"turn_ended":        {"turn ended", ColorTextDim, false},
	"interaction_ended": {"prompt ended", ColorTextDim, false},
}

func argusKindOf(kind string) argusKindStyle {
	if s, ok := argusKinds[kind]; ok {
		return s
	}
	return argusKindStyle{kind, ColorTextDim, false}
}

// argusInnerWidth is the text width inside a message frame of width iw: the
// border and one column of padding on each side.
func argusInnerWidth(iw int) int { return max(iw-4, 10) }

// argusBody draws a message's body at width w, one string per line.
func (m tview) argusBody(msg transcript.ArgusMessage, w int) []string {
	if argusKindOf(msg.Kind).markdown {
		// glamour leaves long words (URLs, paths) and code lines wider than w;
		// the frame would wrap them, past what the fold and the spans count.
		return strings.Split(hardWrap(m.c.m.renderMD(msg.Body, w), w), "\n")
	}
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(msg.Body, "\r", ""), "\n") {
		out = append(out, strings.Split(xansi.Wrap(l, w, ""), "\n")...)
	}
	return out
}

// argusBodyLines counts the body lines of e's messages at band width iw, for
// folding.
func (m tview) argusBodyLines(e transcript.Entry, iw int) int {
	n := 0
	for _, msg := range e.ArgusMessages {
		n += len(m.argusBody(msg, argusInnerWidth(iw)))
	}
	return n
}

// argusWho is who a message's heading names: the child for a notice about
// one, else the sender.
func argusWho(msg transcript.ArgusMessage) (name, session string) {
	if msg.Child != "" {
		return msg.Child, msg.ChildSession
	}
	return msg.From, msg.Session
}

// argusHeadingText is the heading's words, as a copy takes them.
func argusHeadingText(msg transcript.ArgusMessage) string {
	name, session := argusWho(msg)
	parts := []string{argusKindOf(msg.Kind).label, name}
	if session != "" {
		parts = append(parts, session)
	}
	return strings.Join(append(parts, msg.ID), " ")
}

// argusHeading is a message's heading at width w, with clock (if any) on the
// right. When it does not fit, the session ID is cut first.
func argusHeading(msg transcript.ArgusMessage, clock string, w int) string {
	ks := argusKindOf(msg.Kind)
	name, session := argusWho(msg)
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	head := lipgloss.NewStyle().Bold(true).Foreground(ks.color).Render(ks.label) + " " +
		lipgloss.NewStyle().Foreground(ColorTextPrimary).Render(name)
	tail := " " + dim.Render(msg.ID)
	room := w - lipgloss.Width(head) - lipgloss.Width(tail)
	if clock != "" {
		room -= lipgloss.Width(clock) + 1
	}
	if session != "" && room > 1 {
		head += " " + dim.Render(xansi.Truncate(session, room-1, "…"))
	}
	head = xansi.Truncate(head+tail, w-lipgloss.Width(clock)-min(1, len(clock)), "…")
	if clock == "" {
		return head
	}
	return head + strings.Repeat(" ", max(1, w-lipgloss.Width(head)-lipgloss.Width(clock))) + clock
}

// argusBand draws a user entry that holds argus messages: a framed block for
// each message, its body folded with the others' past maxCollapsedLines.
func (m tview) argusBand(e transcript.Entry, expanded bool, iw int) (string, []cardSpan) {
	inner := argusInnerWidth(iw)
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	var blocks []string
	var spans []cardSpan
	if len(e.ArgusMessages) > 1 {
		blocks = append(blocks, dim.Render(fmt.Sprintf("argus · %d messages", len(e.ArgusMessages))))
		spans = append(spans, chromeSpans(1)...)
	}
	budget, hidden := maxCollapsedLines, 0
	for i, msg := range e.ArgusMessages {
		clock := ""
		if i == 0 {
			clock = dim.Render(clockTime(e.Timestamp))
		}
		body := m.argusBody(msg, inner)
		if !expanded {
			shown := min(len(body), budget)
			hidden += len(body) - shown
			budget -= shown
			body = body[:shown]
		}
		content := append([]string{argusHeading(msg, clock, inner)}, body...)
		frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(argusKindOf(msg.Kind).color).
			Padding(0, 1).Width(inner + 4).Render(strings.Join(content, "\n"))
		blocks = append(blocks, frame)
		// A copy takes the heading's words and the body, not the frame.
		spans = append(spans, cardSpan{kind: spanSkip, lines: 1}, cardSpan{kind: spanWhole, lines: 1, src: argusHeadingText(msg)})
		if len(body) > 0 {
			sp := textSpan(strings.Join(body, "\n"), msg.Body)
			sp.framed = true
			spans = append(spans, sp)
		}
		spans = append(spans, cardSpan{kind: spanSkip, lines: 1})
	}
	if hidden > 0 {
		blocks = append(blocks, hiddenHint(hidden))
		spans = append(spans, chromeSpans(1)...)
	}
	return strings.Join(blocks, "\n"), spans
}
