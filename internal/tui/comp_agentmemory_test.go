package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func memoryClient() *pagentClient {
	cl := agentViewClient()
	cl.memory = &api.PAgentMemoryShowResult{Memory: "- likes flat search", User: "- name: Munif", Topics: []string{"topics/billing.md"},
		Usage: 2048, Budget: 24 * 1024}
	cl.commits = []api.PAgentMemoryCommit{
		{Hash: "aaaa1111bbbb", Subject: "add flat search", Source: "capture", When: time.Now().Add(-time.Hour)},
		{Hash: "cccc2222dddd", Subject: "tidy", Source: "tidy", When: time.Now().Add(-2 * time.Hour)},
	}
	cl.props = []api.PAgentMemoryProposal{
		{ID: "n1:p1", Source: "capture", Ops: []api.PAgentMemoryOp{{Action: "add", Text: "prefers tabs"}}, CreatedAt: time.Now()},
		{ID: "n1:p2", Source: "agent", Kind: "instructions", Reason: "be terser", CreatedAt: time.Now()},
	}
	return cl
}

// openedMemory opens unready's memory view from its agent view and loads it.
func openedMemory(t *testing.T, cl *pagentClient) model {
	t.Helper()
	m := loadedAgentView(t, cl)
	m, cmd := upd(m, keyMsg("m"))
	if _, ok := m.main.top().(agentMemoryComp); !ok || cmd == nil {
		t.Fatalf("m opens the memory view: %T", m.main.top())
	}
	m, _ = upd(m, m.loadAgentMemoryCmd("n1:u")())
	return m
}

func pagentParams[T any](cl *pagentClient, method string) (T, int) {
	var p T
	n := 0
	for i, call := range cl.calls {
		if call == method {
			p, n = cl.params[i].(T), n+1
		}
	}
	return p, n
}

func TestAgentMemoryFilesTab(t *testing.T) {
	m := openedMemory(t, memoryClient())
	out := ansi.Strip(m.main.top().view(&ctx{m: &m}, 100, 30))
	for _, want := range []string{"Files", "History", "Review", "MEMORY.md · 2 KB / 24 KB", "likes flat search", "USER.md", "name: Munif", "topics/billing.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("files tab lacks %q:\n%s", want, out)
		}
	}
}

func TestAgentMemoryHistoryUndo(t *testing.T) {
	cl := memoryClient()
	m := openedMemory(t, cl)
	m = typeKeys(m, "gt")
	out := ansi.Strip(m.main.top().view(&ctx{m: &m}, 100, 30))
	if !strings.Contains(out, "add flat search") || !strings.Contains(out, "capture · 1h ago") {
		t.Fatalf("history tab:\n%s", out)
	}
	m = typeKeys(m, "j")
	m = typeKeys(m, "u")
	if !m.main.top().raw(&ctx{m: &m}) {
		t.Fatal("u asks to confirm")
	}
	m, cmd := upd(m, keyMsg("n"))
	if cmd != nil || m.main.top().raw(&ctx{m: &m}) {
		t.Fatal("n cancels the undo")
	}
	m = typeKeys(m, "u")
	m, cmd = upd(m, keyMsg("y"))
	if cmd == nil {
		t.Fatal("y undoes")
	}
	m, _ = upd(m, cmd())
	if p, _ := pagentParams[api.PAgentMemoryUndoParams](cl, api.MethodPAgentMemoryUndo); p != (api.PAgentMemoryUndoParams{AgentID: "n1:u", Commit: "cccc2222dddd"}) {
		t.Fatalf("undo params = %+v", p)
	}
	if m.flash != "undone" {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestAgentMemoryUndoConflictFlash(t *testing.T) {
	cl := memoryClient()
	cl.undoErr = errors.New("revert conflicts with a later change; edit the file by hand")
	m := openedMemory(t, cl)
	m = typeKeys(m, "gt")
	m = typeKeys(m, "u")
	m, cmd := upd(m, keyMsg("y"))
	m, _ = upd(m, cmd())
	if m.flash != "undo: revert conflicts with a later change; edit the file by hand" {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestAgentMemoryReviewAccept(t *testing.T) {
	cl := memoryClient()
	cl.decided = api.PAgentMemoryDecideResult{Applied: 1}
	m := openedMemory(t, cl)
	m = typeKeys(m, "gT")
	out := ansi.Strip(m.main.top().view(&ctx{m: &m}, 100, 30))
	if !strings.Contains(out, "add MEMORY.md: prefers tabs") || !strings.Contains(out, "instructions: be terser") {
		t.Fatalf("review tab:\n%s", out)
	}
	m, cmd := upd(m, keyMsg("a"))
	if cmd == nil {
		t.Fatal("a accepts")
	}
	m, _ = upd(m, cmd())
	if p, _ := pagentParams[api.PAgentMemoryDecideParams](cl, api.MethodPAgentMemoryDecide); p != (api.PAgentMemoryDecideParams{ProposalID: "n1:p1", Accept: true}) {
		t.Fatalf("decide params = %+v", p)
	}
	if m.flash != "accepted: 1 applied" {
		t.Errorf("flash = %q", m.flash)
	}
	m = typeKeys(m, "j")
	m, cmd = upd(m, keyMsg("x"))
	cmd()
	if p, n := pagentParams[api.PAgentMemoryDecideParams](cl, api.MethodPAgentMemoryDecide); n != 2 || p != (api.PAgentMemoryDecideParams{ProposalID: "n1:p2"}) {
		t.Fatalf("reject params = %+v (%d calls)", p, n)
	}
}

func agentMemoryModel() model {
	m := agentViewModel()
	cl := memoryClient()
	m.client = cl
	comp, _ := agentMemoryComp{agentID: "n1:u"}.update(&ctx{m: &m}, m.loadAgentMemoryCmd("n1:u")())
	m.main = m.main.push(comp)
	return m
}

func TestAgentMemoryHistoryCursorFollowsCommit(t *testing.T) {
	cl := memoryClient()
	m := openedMemory(t, cl)
	m = typeKeys(m, "gtj") // History, on "tidy"
	// An undo adds a revert commit at the top.
	cl.commits = append([]api.PAgentMemoryCommit{{Hash: "eeee3333ffff", Subject: "revert", When: time.Now()}}, cl.commits...)
	m, _ = upd(m, m.loadAgentMemoryCmd("n1:u")())
	m = typeKeys(m, "u")
	m, cmd := upd(m, keyMsg("y"))
	cmd()
	if p, _ := pagentParams[api.PAgentMemoryUndoParams](cl, api.MethodPAgentMemoryUndo); p.Commit != "cccc2222dddd" {
		t.Fatalf("undo went to %q, want the selected commit", p.Commit)
	}
}
