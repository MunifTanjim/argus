package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestHistorySessionRowShowsAgentWhenMixed(t *testing.T) {
	s := session.HistorySession{SessionID: "s1", Agent: "codex", LastActivity: "2026-01-01T00:00:00Z"}
	shown := ansi.Strip(historySessionRow(s, false, 78, true))
	if !strings.Contains(shown, "Codex") {
		t.Errorf("showAgent=true should render agent label:\n%s", shown)
	}
	hidden := ansi.Strip(historySessionRow(s, false, 78, false))
	if strings.Contains(hidden, "Codex") {
		t.Errorf("showAgent=false should not render agent label:\n%s", hidden)
	}
}
