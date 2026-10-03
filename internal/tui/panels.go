package tui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"
)

// Panels compose the way Crush does: each draws into its own rectangle of an
// ultraviolet buffer, which clips to the rectangle so styles cannot bleed
// between columns.

type hpanel struct {
	content string
	con     layout.Constraint
	joined  bool // takes the trailing space of the divider before it
}

func fixedPanel(content string, w int) hpanel { return hpanel{content: content, con: layout.Len(w)} }

func flexPanel(content string) hpanel { return hpanel{content: content, con: layout.Fill(1)} }

func joinedPanel(content string, w int) hpanel {
	return hpanel{content: content, con: layout.Len(w), joined: true}
}

func composeH(width, height int, panels ...hpanel) string {
	if width < 1 || height < 1 || len(panels) == 0 {
		return ""
	}
	cons := make([]layout.Constraint, 0, 2*len(panels)-1)
	for i, p := range panels {
		switch {
		case i == 0:
		case p.joined:
			cons = append(cons, layout.Len(dividerWidth-1))
		default:
			cons = append(cons, layout.Len(dividerWidth))
		}
		cons = append(cons, p.con)
	}
	rects := layout.Horizontal(cons...).Split(uv.Rect(0, 0, width, height))

	scr := uv.NewScreenBuffer(width, height)
	for i, p := range panels {
		uv.NewStyledString(p.content).Draw(scr, rects[2*i]) // panels at even indices
	}
	for i := 1; i < len(panels); i++ {
		drawVDivider(scr, rects[2*i-1]) // dividers at odd indices
	}
	return scr.Render()
}

func drawVDivider(scr uv.ScreenBuffer, rect uv.Rectangle) {
	bar := lipgloss.NewStyle().Foreground(ColorBorder).Render("│")
	line := " " + bar + strings.Repeat(" ", rect.Dx()-2)
	content := strings.TrimRight(strings.Repeat(line+"\n", rect.Dy()), "\n")
	uv.NewStyledString(content).Draw(scr, rect)
}
