package tui

import (
	"image/color"

	lipgloss "charm.land/lipgloss/v2"
)

// StyledIcon pairs an icon glyph with its default foreground color.
// Requires a Nerd Font patched terminal font (e.g. JetBrains Mono Nerd Font).
// Ported from kylesnowschwartz/tail-claude icons.go.
type StyledIcon struct {
	Glyph string
	Color color.Color
}

// Render returns the icon styled with its default color.
func (s StyledIcon) Render() string {
	return lipgloss.NewStyle().Foreground(s.Color).Render(s.Glyph)
}

// RenderBold returns the icon styled bold with its default color.
func (s StyledIcon) RenderBold() string {
	return lipgloss.NewStyle().Bold(true).Foreground(s.Color).Render(s.Glyph)
}

// WithColor returns the icon styled with an override color.
func (s StyledIcon) WithColor(c color.Color) string {
	return lipgloss.NewStyle().Foreground(c).Render(s.Glyph)
}

// Shared glyphs, named so reuse is explicit. Unicode escapes guard against silent
// corruption by LLM tools.
const (
	glyphRobot        = "\U000F167A" // nf-md-robot_outline
	glyphWrench       = "\U000F0BE0" // nf-md-wrench_outline
	glyphFolderSearch = "\U000F0968" // nf-md-folder_search
	glyphPenNib       = "\uEE75"     // nf-fa-pen_nib
)

// toolIcons groups per-category icons for the tool item rows.
type toolIcons struct {
	Err   StyledIcon
	Ok    StyledIcon
	Read  StyledIcon
	Edit  StyledIcon
	Write StyledIcon
	Bash  StyledIcon
	Grep  StyledIcon
	Glob  StyledIcon
	Task  StyledIcon
	Todo  StyledIcon
	Skill StyledIcon
	Web   StyledIcon
	Misc  StyledIcon
}

// taskIcons groups status glyphs for task/subagent state.
type taskIcons struct {
	Done    StyledIcon
	Active  StyledIcon
	Pending StyledIcon
}

// iconSet holds every TUI icon, grouped by domain. Codepoints from Font Awesome
// and Material Design.
type iconSet struct {
	Branch    StyledIcon
	Chat      StyledIcon
	Claude    StyledIcon
	Clock     StyledIcon
	Collapsed StyledIcon
	Dot       StyledIcon
	DrillDown StyledIcon
	Ellipsis  StyledIcon
	Expanded  StyledIcon
	Help      StyledIcon
	Memory    StyledIcon
	Node      StyledIcon
	Output    StyledIcon
	Selected  StyledIcon
	Session   StyledIcon
	Shell     StyledIcon
	Skill     StyledIcon
	Subagent  StyledIcon
	System    StyledIcon
	SystemErr StyledIcon
	Teammate  StyledIcon
	Thinking  StyledIcon
	Token     StyledIcon
	User      StyledIcon
	Tool      toolIcons
	Task      taskIcons
}

// Icon is the single source of truth for all TUI icons.
// Initialized by initIcons() after initTheme() resolves colors.
var Icon iconSet

// Plain glyphs -- used as raw strings (never styled via StyledIcon).
const (
	GlyphHRule            = "─"      // box drawing horizontal (compact separators)
	GlyphAccentBar        = "┃"      // detail item gutter (unfocused)
	GlyphAccentBarFocused = "▌"      // detail item gutter (focused)
	GlyphBeadFull         = "\uEABC" // nf-cod-circle (activity indicator bead)
)

// SpinnerFrames is a 10-frame braille spinner used for ongoing indicators.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// initIcons builds all icon values from resolved theme colors; call after initTheme.
// Glyphs use explicit Unicode escapes: Nerd Font PUA codepoints are easily dropped
// when LLM tools round-trip the file.
func initIcons() {
	Icon = iconSet{
		Branch:    StyledIcon{"\uF418", ColorGitBranch}, // nf-pl-branch
		Chat:      StyledIcon{"\uF086", ColorTextDim},   // nf-fa-comments
		Claude:    StyledIcon{glyphRobot, ColorInfo},
		Clock:     StyledIcon{"\uF017", ColorTextDim},           // nf-fa-clock
		Collapsed: StyledIcon{"\uF054", ColorTextDim},           // nf-fa-chevron_right
		Dot:       StyledIcon{"·", ColorTextMuted},              // middle dot
		DrillDown: StyledIcon{"\uF061", ColorAccent},            // nf-fa-arrow_right
		Ellipsis:  StyledIcon{"…", ColorTextDim},                // horizontal ellipsis
		Expanded:  StyledIcon{"\uF078", ColorTextPrimary},       // nf-fa-chevron_down
		Output:    StyledIcon{"\U000F0182", ColorAccent},        // nf-md-comment_outline
		Selected:  StyledIcon{"│", ColorAccent},                 // box drawing vertical
		Help:      StyledIcon{"\U000F02D7", ColorAccent},        // nf-md-help_circle_outline
		Memory:    StyledIcon{"\U000F01C0", ColorTextDim},       // nf-md-book_open_variant
		Node:      StyledIcon{"\U000F0429", ColorTextSecondary}, // nf-md-server
		Session:   StyledIcon{"\U000F0237", ColorTextDim},       // nf-md-fingerprint
		Shell:     StyledIcon{"\uF120", ColorToolBash},          // nf-fa-terminal
		Skill:     StyledIcon{"\uF19D", ColorToolSkill},         // nf-fa-graduation_cap
		Subagent:  StyledIcon{glyphRobot, ColorAccent},
		System:    StyledIcon{"\uF120", ColorTextMuted}, // nf-fa-terminal
		SystemErr: StyledIcon{"\uF06A", ColorError},     // nf-fa-circle_exclamation
		Teammate:  StyledIcon{glyphRobot, ColorAccent},
		Thinking:  StyledIcon{"\uF0EB", ColorTextDim},       // nf-fa-lightbulb
		Token:     StyledIcon{"\uEDE8", ColorTextDim},       // nf-fa-coins
		User:      StyledIcon{"\uF007", ColorTextSecondary}, // nf-fa-user
		Tool: toolIcons{
			Err:   StyledIcon{glyphWrench, ColorError},
			Ok:    StyledIcon{glyphWrench, ColorTextDim},
			Read:  StyledIcon{"\uE28B", ColorToolRead}, // nf-fae-book_open_o
			Edit:  StyledIcon{glyphPenNib, ColorToolEdit},
			Write: StyledIcon{glyphPenNib, ColorToolWrite},
			Bash:  StyledIcon{glyphWrench, ColorToolBash},
			Grep:  StyledIcon{glyphFolderSearch, ColorToolGrep},
			Glob:  StyledIcon{glyphFolderSearch, ColorToolGlob},
			Task:  StyledIcon{glyphRobot, ColorToolTask},
			Todo:  StyledIcon{"\U000F0755", ColorToolEdit}, // nf-md-format_list_checks
			Skill: StyledIcon{"", ColorToolSkill},         // nf-fa-graduation_cap
			Web:   StyledIcon{"\U000F059F", ColorToolWeb},  // nf-md-web
			Misc:  StyledIcon{glyphWrench, ColorToolOther},
		},
		Task: taskIcons{
			Done:    StyledIcon{"\U000F0133", ColorOngoing},   // nf-md-checkbox_marked_circle
			Active:  StyledIcon{"\U000F0996", ColorAccent},    // nf-md-progress_clock
			Pending: StyledIcon{"\U000F0130", ColorTextMuted}, // nf-md-checkbox_blank_circle_outline
		},
	}
}

func toolDisplayName(name string) string {
	if meta, ok := toolRegistry[name]; ok && meta.display != "" {
		return meta.display
	}
	return name
}

func toolIcon(name string, isError bool) StyledIcon {
	if isError {
		return Icon.Tool.Err
	}
	if meta, ok := toolRegistry[name]; ok {
		return categoryIcon(meta.category)
	}
	return Icon.Tool.Misc
}
