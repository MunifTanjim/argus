package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestClampWidth(t *testing.T) {
	if got := clampWidth(10, 20, 60); got != 20 {
		t.Errorf("clampWidth below min = %d, want 20", got)
	}
	if got := clampWidth(80, 20, 60); got != 60 {
		t.Errorf("clampWidth above max = %d, want 60", got)
	}
	if got := clampWidth(40, 20, 60); got != 40 {
		t.Errorf("clampWidth in range = %d, want 40", got)
	}
}

func TestComposeHDrawsPanelsAndDivider(t *testing.T) {
	out := composeH(40, 3, fixedPanel("LEFT", 10), flexPanel("RIGHT"))
	if out == "" {
		t.Fatal("composeH returned empty")
	}
	for _, want := range []string{"LEFT", "RIGHT", "│"} {
		if !strings.Contains(out, want) {
			t.Errorf("composeH output missing %q", want)
		}
	}
	out3 := composeH(60, 3, fixedPanel("L", 10), flexPanel("C"), fixedPanel("R", 10))
	for _, want := range []string{"L", "C", "R"} {
		if !strings.Contains(out3, want) {
			t.Errorf("3-panel composeH missing %q", want)
		}
	}
}

func TestComposeHDegenerate(t *testing.T) {
	if composeH(0, 5, flexPanel("x")) != "" {
		t.Error("zero width should render empty")
	}
	if composeH(10, 5) != "" {
		t.Error("no panels should render empty")
	}
}

func TestFooterKeepsScreenMargin(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 100, 30
	long := helpAs(projectsKeys.Help, "?", strings.Repeat("x", m.width-5)) // one column past the margin
	if w := lipgloss.Width(m.footer(long)); w > m.width-2*screenMargin {
		t.Errorf("footer is %d wide; a centered footer must leave %d columns each side of %d", w, screenMargin, m.width)
	}
}
