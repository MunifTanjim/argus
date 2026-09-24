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
}

func fixedPanel(content string, w int) hpanel { return hpanel{content, layout.Len(w)} }

func flexPanel(content string) hpanel { return hpanel{content, layout.Fill(1)} }

func composeH(width, height int, panels ...hpanel) string {
	if width < 1 || height < 1 || len(panels) == 0 {
		return ""
	}
	cons := make([]layout.Constraint, 0, 2*len(panels)-1)
	for i, p := range panels {
		if i > 0 {
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
	line := " " + bar + " "
	content := strings.TrimRight(strings.Repeat(line+"\n", rect.Dy()), "\n")
	uv.NewStyledString(content).Draw(scr, rect)
}
