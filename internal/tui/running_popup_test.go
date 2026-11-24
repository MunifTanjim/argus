package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

var twoWorkflows = []session.RunningWorkflow{
	{RunID: "wf1", Name: "final-branch-review", Done: 3, Total: 7, Since: "2026-10-16T09:00:00Z"},
	{RunID: "wf2", Since: "2026-10-16T08:58:55Z"},
}

func TestRunningWorkflowsCount(t *testing.T) {
	m := withSession(sessionModel(nil), func(s *session.Session) {
		s.Summary = &session.Summary{ModelName: "Opus 5.5", Effort: "high"}
	})
	m.width = 120
	_, before, _ := m.dockRules(80, ColorBorder)
	m = withSession(m, func(s *session.Session) { s.Workflows = twoWorkflows })
	if n := m.runningCount(runningWorkflows); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	_, bottom, _ := m.dockRules(80, ColorBorder)
	if got := ansi.Strip(bottom); !strings.HasPrefix(got, "── "+Icon.Workflow.Glyph+" 2 ") {
		t.Fatalf("bottom rule should lead with the workflow count: %q", got)
	}
	m = withSession(m, func(s *session.Session) { s.Workflows = nil })
	if _, bottom, _ := m.dockRules(80, ColorBorder); bottom != before {
		t.Fatalf("nothing running: the rule should be unchanged: %q", ansi.Strip(bottom))
	}
	if runningWorkflows.label(0) != "" {
		t.Fatalf("label(0) = %q, want empty", runningWorkflows.label(0))
	}
}

func TestRunningWorkflowRows(t *testing.T) {
	m := withSession(sessionModel(nil), func(s *session.Session) { s.Workflows = twoWorkflows })
	rows := m.runningRows(runningWorkflows)
	if len(rows) != 2 || rows[0].text != "final-branch-review · 3/7" || rows[1].text != "Workflow · 0/0" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].since != twoWorkflows[0].Since || rows[0].open == nil {
		t.Fatalf("row should carry since and open: %+v", rows[0])
	}
}

func TestOpenWorkflowCommand(t *testing.T) {
	m := withSession(sessionModel(nil), func(s *session.Session) { s.Workflows = twoWorkflows })
	m, cmd := upd(m, cmdMsg(sessionKeys.Workflows.name))
	runCmd(cmd)
	if _, ok := runningFront(m, runningWorkflows); !ok {
		t.Fatalf("open workflow should open the popup; front = %T", m.popups.front())
	}
}
