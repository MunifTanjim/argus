package tui

import (
	"image/color"

	lipgloss "charm.land/lipgloss/v2"
)

// -- Colors ---------------------------------------------------------------
// Colors resolve once at init via initTheme(hasDarkBg), before alt-screen.
// Gotcha: ANSI 7/15 (white) are invisible on light backgrounds -- never use
// them for Light values.
//
// Ported from kylesnowschwartz/tail-claude theme.go.

var (
	// Text hierarchy
	ColorTextPrimary   color.Color
	ColorTextSecondary color.Color
	ColorTextDim       color.Color
	ColorTextMuted     color.Color

	// Accents
	ColorAccent color.Color
	ColorError  color.Color
	ColorInfo   color.Color
	ColorFocus  color.Color // bright cyan: the focus/selection highlight

	// Surfaces
	ColorBorder color.Color

	// Model family
	ColorModelOpus   color.Color
	ColorModelSonnet color.Color
	ColorModelHaiku  color.Color

	// Token highlight
	ColorTokenHigh color.Color

	// Ongoing indicator
	ColorOngoing color.Color

	// Context usage thresholds
	ColorContextOk   color.Color // green: <50%
	ColorContextWarn color.Color // yellow/orange: 50-80%
	ColorContextCrit color.Color // red: >80%

	// Permission mode pill backgrounds
	ColorPillBypass      color.Color // red: bypassPermissions
	ColorPillAcceptEdits color.Color // purple: acceptEdits
	ColorPillPlan        color.Color // green: plan

	// Search
	ColorSearchHighlightFg color.Color
	ColorSearchHighlightBg color.Color

	// Diff
	ColorDiffAdd color.Color
	ColorDiffDel color.Color

	// Git
	ColorGitBranch color.Color

	// Tool category colors (per-category icons in the item rows).
	ColorToolRead  color.Color
	ColorToolEdit  color.Color
	ColorToolWrite color.Color
	ColorToolBash  color.Color
	ColorToolGrep  color.Color
	ColorToolGlob  color.Color
	ColorToolTask  color.Color
	ColorToolSkill color.Color
	ColorToolWeb   color.Color
	ColorToolOther color.Color

	// Team member colors (8 named colors assignable to subagents).
	ColorTeamBlue   color.Color
	ColorTeamGreen  color.Color
	ColorTeamRed    color.Color
	ColorTeamYellow color.Color
	ColorTeamPurple color.Color
	ColorTeamCyan   color.Color
	ColorTeamOrange color.Color
	ColorTeamPink   color.Color
)

// -- Semantic text styles -----------------------------------------------------
// Safe to chain (.Width(), .Padding(), etc.): lipgloss styles are immutable
// value types, each method returns a copy.

var (
	StylePrimaryBold     lipgloss.Style
	StyleSecondary       lipgloss.Style
	StyleSecondaryBold   lipgloss.Style
	StyleDim             lipgloss.Style
	StyleMuted           lipgloss.Style
	StyleAccentBold      lipgloss.Style
	StyleErrorBold       lipgloss.Style
	StyleSearchHighlight lipgloss.Style
)

// initTheme resolves all colors for the detected background and rebuilds styles.
// Called once in Run() before Bubble Tea starts.
func initTheme(hasDarkBg bool) {
	ld := lipgloss.LightDark(hasDarkBg)

	// Text hierarchy
	ColorTextPrimary = ld(lipgloss.Color("0"), lipgloss.Color("252"))
	ColorTextSecondary = ld(lipgloss.Color("8"), lipgloss.Color("245"))
	ColorTextDim = ld(lipgloss.Color("242"), lipgloss.Color("243"))
	ColorTextMuted = ld(lipgloss.Color("245"), lipgloss.Color("240"))

	// Accents
	ColorAccent = ld(lipgloss.Color("4"), lipgloss.Color("75"))
	ColorError = ld(lipgloss.Color("1"), lipgloss.Color("196"))
	ColorInfo = ld(lipgloss.Color("4"), lipgloss.Color("69"))
	ColorFocus = ld(lipgloss.Color("6"), lipgloss.Color("51")) // cyan / bright cyan

	// Surfaces
	ColorBorder = ld(lipgloss.Color("250"), lipgloss.Color("60"))

	// Model family
	ColorModelOpus = ld(lipgloss.Color("1"), lipgloss.Color("204"))
	ColorModelSonnet = ld(lipgloss.Color("4"), lipgloss.Color("75"))
	ColorModelHaiku = ld(lipgloss.Color("2"), lipgloss.Color("114"))

	// Token highlight
	ColorTokenHigh = ld(lipgloss.Color("3"), lipgloss.Color("208"))

	// Ongoing indicator
	ColorOngoing = ld(lipgloss.Color("2"), lipgloss.Color("76"))

	// Context usage thresholds
	ColorContextOk = ld(lipgloss.Color("2"), lipgloss.Color("114"))
	ColorContextWarn = ld(lipgloss.Color("3"), lipgloss.Color("208"))
	ColorContextCrit = ld(lipgloss.Color("1"), lipgloss.Color("196"))

	// Permission mode pill backgrounds
	ColorPillBypass = ld(lipgloss.Color("1"), lipgloss.Color("196"))
	ColorPillAcceptEdits = ld(lipgloss.Color("5"), lipgloss.Color("135"))
	ColorPillPlan = ld(lipgloss.Color("2"), lipgloss.Color("114"))

	// Search highlight (yellow/black -- stands out on both dark and light)
	ColorSearchHighlightFg = ld(lipgloss.Color("0"), lipgloss.Color("0"))
	ColorSearchHighlightBg = ld(lipgloss.Color("11"), lipgloss.Color("3"))

	// Diff
	ColorDiffAdd = ld(lipgloss.Color("2"), lipgloss.Color("114"))
	ColorDiffDel = ld(lipgloss.Color("1"), lipgloss.Color("204"))

	// Git
	ColorGitBranch = ld(lipgloss.Color("5"), lipgloss.Color("135"))

	// Tool category colors (adaptive light/dark).
	ColorToolRead = ld(lipgloss.Color("6"), lipgloss.Color("80"))   // cyan
	ColorToolEdit = ld(lipgloss.Color("2"), lipgloss.Color("114"))  // green
	ColorToolWrite = ld(lipgloss.Color("5"), lipgloss.Color("177")) // purple
	ColorToolBash = ld(lipgloss.Color("3"), lipgloss.Color("208"))  // gold/orange
	ColorToolGrep = ld(lipgloss.Color("3"), lipgloss.Color("215"))  // amber
	ColorToolGlob = ld(lipgloss.Color("3"), lipgloss.Color("215"))  // amber
	ColorToolTask = ld(lipgloss.Color("12"), lipgloss.Color("39"))  // bright blue (distinct from the accent focus color)
	ColorToolSkill = ld(lipgloss.Color("5"), lipgloss.Color("141")) // violet
	ColorToolWeb = ld(lipgloss.Color("6"), lipgloss.Color("80"))    // cyan
	ColorToolOther = ColorTextDim

	// Team member colors
	ColorTeamBlue = ld(lipgloss.Color("4"), lipgloss.Color("75"))
	ColorTeamGreen = ld(lipgloss.Color("2"), lipgloss.Color("114"))
	ColorTeamRed = ld(lipgloss.Color("1"), lipgloss.Color("204"))
	ColorTeamYellow = ld(lipgloss.Color("3"), lipgloss.Color("220"))
	ColorTeamPurple = ld(lipgloss.Color("5"), lipgloss.Color("177"))
	ColorTeamCyan = ld(lipgloss.Color("6"), lipgloss.Color("80"))
	ColorTeamOrange = ld(lipgloss.Color("3"), lipgloss.Color("208"))
	ColorTeamPink = ld(lipgloss.Color("5"), lipgloss.Color("211"))

	// Rebuild styles with resolved colors.
	StylePrimaryBold = lipgloss.NewStyle().Bold(true).Foreground(ColorTextPrimary)
	StyleSecondary = lipgloss.NewStyle().Foreground(ColorTextSecondary)
	StyleSecondaryBold = lipgloss.NewStyle().Bold(true).Foreground(ColorTextSecondary)
	StyleDim = lipgloss.NewStyle().Foreground(ColorTextDim)
	StyleMuted = lipgloss.NewStyle().Foreground(ColorTextMuted)
	StyleAccentBold = lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	StyleErrorBold = lipgloss.NewStyle().Bold(true).Foreground(ColorError)
	StyleSearchHighlight = lipgloss.NewStyle().Bold(true).
		Foreground(ColorSearchHighlightFg).Background(ColorSearchHighlightBg)
}
