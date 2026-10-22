package claudecode

import (
	"context"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter"
)

type PaneController = adapter.PaneController

// PrepareTextInput readies a Claude Code pane for injected text: it exits any
// tmux copy/view mode so keystrokes reach the program, then sends `i`+BSpace to
// land in vim INSERT (if vim mode is on) from any starting state without erasing
// anything — assumes an empty prompt (true for argus's composer sends).
//
// Screen detection of the INSERT indicator is deliberately avoided: Claude's
// "-- INSERT --" frame wraps unpredictably by pane width, so no substring match
// is reliable. `i`+BSpace is correct regardless, at the cost of one redundant
// keystroke pair when already in INSERT.
func PrepareTextInput(ctx context.Context, pc PaneController, paneID string) error {
	inMode, err := pc.PaneInMode(ctx, paneID)
	if err != nil {
		return err
	}
	if inMode {
		if err := pc.CancelMode(ctx, paneID); err != nil {
			return err
		}
	}
	return pc.SendKeys(ctx, paneID, "i", "BSpace")
}

var _ adapter.DraftReader = ccAdapter{}

// HasDraft reports whether the prompt line of a captured Claude Code pane holds
// text: the last line that starts with the prompt mark, "❯" or the older ">"
// inside a "│" box, and sits right above the input box's lower rule or border.
// A prompt echoed in the transcript is not the input line. The empty-prompt
// placeholder `Try "..."` is not a draft.
func (ccAdapter) HasDraft(screen string) bool {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if i+1 >= len(lines) || !inputBoxEdge(lines[i+1]) {
			continue
		}
		line := strings.TrimSpace(strings.ReplaceAll(lines[i], "\u00a0", " "))
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "│"), "│"))
		var rest string
		switch {
		case strings.HasPrefix(line, "❯"):
			rest = strings.TrimPrefix(line, "❯")
		case strings.HasPrefix(line, ">"):
			rest = strings.TrimPrefix(line, ">")
		default:
			continue
		}
		rest = strings.TrimSpace(rest)
		return rest != "" && !strings.HasPrefix(rest, `Try "`)
	}
	return false
}

// inputBoxEdge reports whether line is the rule or border under Claude Code's
// input line.
func inputBoxEdge(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "─") || strings.HasPrefix(line, "╰")
}
