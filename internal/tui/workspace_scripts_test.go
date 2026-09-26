package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func withSetup(m model, wsIndex int, run *api.ScriptRun) model {
	m.projects.tree[0].Workspaces[wsIndex].Setup = run
	m.projects.rebuild()
	return m
}

func rowLine(view, text string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, text) {
			return l
		}
	}
	return ""
}

func TestTreeShowsSetupState(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = withSetup(m, 1, &api.ScriptRun{State: "running", Command: "pnpm install"})
	row := rowLine(ansi.Strip(m.View().Content), "repo-feat")
	if !strings.Contains(row, "setting up…") {
		t.Errorf("a running setup should show on the n1:w2 row: %q", row)
	}
	if !m.anySetupRunning() {
		t.Error("a running setup should keep the spinner going")
	}
	if !strings.Contains(row, "○ 1") {
		t.Errorf("activity badge should stay on the n1:w2 row while setup is running: %q", row)
	}
	m = withSetup(m, 1, &api.ScriptRun{State: "failed", Command: "pnpm install", ExitCode: 1})
	row = rowLine(ansi.Strip(m.View().Content), "repo-feat")
	if !strings.Contains(row, "setup failed") {
		t.Errorf("a failed setup should show on the n1:w2 row: %q", row)
	}
	if !strings.Contains(row, "○ 1") {
		t.Errorf("activity badge should stay on the n1:w2 row while setup has failed: %q", row)
	}
	m = withSetup(m, 1, &api.ScriptRun{State: "ok", Command: "pnpm install"})
	if out := ansi.Strip(m.View().Content); strings.Contains(out, "setup") {
		t.Errorf("a passed setup should show nothing:\n%s", out)
	}
}

func TestPaneShowsSetupBlock(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = withSetup(m, 1, &api.ScriptRun{State: "failed", Command: "pnpm install", ExitCode: 1, OutputTail: "ERR_PNPM_NO_LOCKFILE\nexit"})
	m.projects.selectRow("n1:w2")
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"setup failed (exit 1) · pnpm install", "ERR_PNPM_NO_LOCKFILE", "S runs setup again · L shows the full log"} {
		if !strings.Contains(out, want) {
			t.Errorf("pane lacks %q:\n%s", want, out)
		}
	}
}

func withScripts(m model, setup, teardown string) model {
	m.projects.tree[0].Scripts = &api.ProjectScripts{Setup: setup, Teardown: teardown}
	m.projects.rebuild()
	return m
}

func TestCreatePickerShowsSetup(t *testing.T) {
	m := withScripts(createTestModel(t), "pnpm install", "")
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "setup: pnpm install") {
		t.Errorf("the create picker should show the setup command:\n%s", out)
	}
	m.projects.create.creating = true
	m, _ = upd(m, createDoneMsg{seq: m.projects.create.seq, res: api.WorkspaceCreateResult{WorkspaceID: "n1:w9", Dir: "/repo/.worktrees/login", Setup: "pnpm install"}, source: api.SourceNew})
	if m.flash != "created workspace login · setting up" {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestRunSetupKey(t *testing.T) {
	m := withScripts(projectsTestModel(), "pnpm install", "")
	m.width, m.height = 120, 30
	rc := &recordingClient{}
	m.client = rc
	m.projects.selectRow("n1:w2")
	m, cmd := upd(m, keyMsg("S"))
	runCmd(cmd)
	if p, ok := paramsFor(m, api.MethodWorkspaceRunSetup).(api.WorkspaceRef); !ok || p.WorkspaceID != "n1:w2" || m.flash != "running setup" {
		t.Errorf("S should run setup on n1:w2: calls=%v flash=%q", rc.calls, m.flash)
	}
	m.projects.focus = focusPane
	m, _ = upd(m, keyMsg("S"))
	if m.flash != "manage keys work in the tree · esc to go there" {
		t.Errorf("S in the pane should hint the tree: %q", m.flash)
	}
}

func TestSetupLogKeyOpensTheLog(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &recordingClient{}
	m.projects.selectRow("n1:w2")
	m, cmd := upd(m, keyMsg("L"))
	runCmd(cmd)
	if p, ok := paramsFor(m, api.MethodWorkspaceSetupLog).(api.WorkspaceRef); !ok || p.WorkspaceID != "n1:w2" {
		t.Fatalf("L should fetch the setup log")
	}
	m, _ = upd(m, setupLogMsg{ws: "n1:w2", output: "installing\ndone\n"})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "setup log · repo-feat") || !strings.Contains(out, "installing") {
		t.Errorf("the log should open in the file view:\n%s", out)
	}
}

func TestRemovePromptNamesTeardown(t *testing.T) {
	m := withScripts(projectsTestModel(), "", "docker compose down")
	m.width, m.height = 120, 30
	delete(m.sessions, "n1:s2")
	m.projects.selectRow("n1:w2")
	m, _ = upd(m, keyMsg("x"))
	if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "remove workspace repo-feat (feature)? runs teardown: docker compose down · y/n") {
		t.Errorf("remove prompt = %q", f)
	}
}

func TestForcedRemoveFlashesTeardownWarning(t *testing.T) {
	m := projectsTestModel()
	m.client = &warningRemoveClient{}
	msg := m.removeWorkspaceCmd("n1:w2", true)().(projectsActionMsg)
	if msg.ok != "removed repo-feat · teardown failed (exit 4): cannot stop db" {
		t.Errorf("ok = %q", msg.ok)
	}
}

type warningRemoveClient struct{ recordingClient }

func (c *warningRemoveClient) Call(method string, params, out any) error {
	if r, ok := out.(*api.WorkspaceRemoveResult); ok {
		r.Warning = "teardown failed (exit 4): cannot stop db"
	}
	return nil
}

func TestSpawnNotesRunningSetup(t *testing.T) {
	m := withSetup(projectsTestModel(), 1, &api.ScriptRun{State: "running", Command: "pnpm install"})
	m.width, m.height = 120, 30
	m.beginPresetSpawn("n1", "/repo-feat", "")
	m, _ = upd(m, spawnAgentsMsg{nodeID: "n1", agents: []api.AgentInfo{{ID: "claude"}}})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "setup is still running") {
		t.Errorf("the spawn prompt should note the running setup:\n%s", out)
	}
}

func TestProjectChangedRefetches(t *testing.T) {
	m := projectsTestModel()
	rc := &recordingClient{}
	m.client = rc
	runCmd(m.applyEvent(api.Notification{Method: api.MethodProjectChanged}))
	if !m.projects.loading || len(rc.calls) == 0 || rc.calls[len(rc.calls)-1] != api.MethodProjectList {
		t.Errorf("project.changed should refetch the tree: calls=%v", rc.calls)
	}
	if cmd := m.applyEvent(api.Notification{Method: api.MethodProjectChanged}); cmd != nil {
		t.Error("a second notice during a fetch should not start another")
	}
}

// projectReplies runs cmd's project fetches, skipping timers, and returns their replies.
func projectReplies(cmd tea.Cmd) []projectsTreeMsg {
	if cmd == nil {
		return nil
	}
	var out []projectsTreeMsg
	var walk func(c tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case msg := <-done:
			switch msg := msg.(type) {
			case projectsTreeMsg:
				out = append(out, msg)
			case tea.BatchMsg:
				for _, cc := range msg {
					walk(cc)
				}
			}
		case <-time.After(100 * time.Millisecond): // a timer, not a fetch
		}
	}
	walk(cmd)
	return out
}

func TestProjectChangedDuringAFetchRefetchesOnceAfterIt(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	first := projectReplies(m.applyEvent(api.Notification{Method: api.MethodProjectChanged}))
	if len(first) != 1 {
		t.Fatalf("project.changed should fetch once, got %d", len(first))
	}
	for range 2 {
		if cmd := m.applyEvent(api.Notification{Method: api.MethodProjectChanged}); cmd != nil {
			t.Fatal("a notice during a fetch should wait for it")
		}
	}
	m, cmd := upd(m, first[0])
	follow := projectReplies(cmd)
	if len(follow) != 1 || !m.projects.loading {
		t.Fatalf("the reply should start exactly one follow-up fetch: got %d, loading=%v", len(follow), m.projects.loading)
	}
	m, cmd = upd(m, follow[0])
	if n := len(projectReplies(cmd)); n != 0 || m.projects.loading {
		t.Errorf("the follow-up reply should end the fetching: %d more, loading=%v", n, m.projects.loading)
	}
}

func TestEveryProjectFetchSharesTheGate(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	m, cmd := upd(m, projectsActionMsg{verb: "rename"})
	replies := projectReplies(cmd)
	if len(replies) != 1 || !m.projects.loading {
		t.Fatalf("an action should fetch the tree and mark it loading: %d, loading=%v", len(replies), m.projects.loading)
	}
	m, cmd = upd(m, createDoneMsg{seq: m.projects.create.seq + 1, res: api.WorkspaceCreateResult{Dir: "/repo/.worktrees/x"}})
	if n := len(projectReplies(cmd)); n != 0 {
		t.Fatalf("a create during a fetch should wait for it, got %d fetches", n)
	}
	m, cmd = upd(m, replies[0])
	if n := len(projectReplies(cmd)); n != 1 {
		t.Errorf("the create should refetch once after the reply, got %d", n)
	}
}

func TestStaleTreeReplyKeepsLoading(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	replies := projectReplies(m.applyEvent(api.Notification{Method: api.MethodProjectChanged}))
	stale := replies[0]
	stale.seq--
	m, _ = upd(m, stale)
	if !m.projects.loading {
		t.Error("an older reply must not end a newer fetch")
	}
}

func TestTreeReplyResumesTheSpinner(t *testing.T) {
	m := projectsTestModel()
	tree := []api.ProjectNode{m.projects.tree[0]}
	tree[0].Workspaces = append([]api.WorkspaceNode{}, tree[0].Workspaces...)
	tree[0].Workspaces[1].Setup = &api.ScriptRun{State: "running", Command: "pnpm install"}
	m, _ = upd(m, projectsTreeMsg{tree: tree})
	if !m.spinning {
		t.Error("a tree with a running setup should start the spinner")
	}
}

func TestCommandLineShowsOneCleanLine(t *testing.T) {
	if got := commandLine("pnpm install\npnpm build\n"); got != "pnpm install …" {
		t.Errorf("two-line command = %q", got)
	}
	if got := commandLine("echo \x1b[31mred\x1b[0m\tdone\a"); got != "echo red done" {
		t.Errorf("escape sequences and controls should go: %q", got)
	}
}

func TestMultiLineCommandsStayOnOneLine(t *testing.T) {
	cmd := "pnpm install\n\x1b[2Jpnpm build"
	m := withScripts(projectsTestModel(), cmd, cmd)
	m.width, m.height = 120, 30
	delete(m.sessions, "n1:s2")
	m.projects.selectRow("n1:w2")
	m, _ = upd(m, keyMsg("x"))
	if f := m.projectsFooter(); !strings.Contains(ansi.Strip(f), "runs teardown: pnpm install … · y/n") || strings.Contains(f, "\x1b[2J") {
		t.Errorf("remove prompt = %q", f)
	}
	m, _ = upd(m, keyMsg("n"))
	m = withSetup(m, 1, &api.ScriptRun{State: "running", Command: cmd})
	if out := m.View().Content; !strings.Contains(ansi.Strip(out), "setup running · pnpm install …") || strings.Contains(out, "\x1b[2J") {
		t.Errorf("setup block:\n%s", ansi.Strip(out))
	}
}

func TestSetupBlockWithoutCommand(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = withSetup(m, 1, &api.ScriptRun{State: "failed", ExitCode: -1, OutputTail: ".argus/settings.toml: bad"})
	m.projects.selectRow("n1:w2")
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "setup failed\n") || strings.Contains(out, "exit -1") || strings.Contains(out, "setup failed (") {
		t.Errorf("a failed setup with no command should read \"setup failed\":\n%s", out)
	}
}

func TestSetupBlockHintIsTruncated(t *testing.T) {
	m := projectsTestModel()
	m = withSetup(m, 1, &api.ScriptRun{State: "failed", Command: "x", ExitCode: 1})
	m.projects.selectRow("n1:w2")
	for _, l := range strings.Split(m.setupBlock(20), "\n") {
		if w := ansi.StringWidth(l); w > 20 {
			t.Errorf("line %q is %d wide, want at most 20", ansi.Strip(l), w)
		}
	}
}

func TestSetupLogIsCleaned(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &recordingClient{}
	m.projects.selectRow("n1:w2")
	m, _ = upd(m, keyMsg("L"))
	m, _ = upd(m, setupLogMsg{ws: "n1:w2", output: "10%\r50%\r100%\n\x1b[32mdone\x1b[0m\r\n"})
	if got := m.projects.fileView.lines; len(got) != 2 || got[0] != "100%" || got[1] != "done" {
		t.Errorf("log lines = %q", got)
	}
	m, _ = upd(m, setupLogMsg{ws: "n1:w2", output: ""})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "no setup run") {
		t.Errorf("an empty log should say so:\n%s", out)
	}
	m = withSetup(m, 1, &api.ScriptRun{State: "ok", Command: "true"})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "no output") || strings.Contains(out, "no setup run") {
		t.Errorf("a silent setup ran, so its log has no output:\n%s", out)
	}
}

func TestForceRemovePromptNamesTeardown(t *testing.T) {
	m := withScripts(projectsTestModel(), "", "docker compose down")
	m.width, m.height = 120, 30
	delete(m.sessions, "n1:s2")
	m.projects.selectRow("n1:w2")
	m, _ = upd(m, keyMsg("X"))
	if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "force-remove workspace repo-feat (feature)? uncommitted changes are lost · runs teardown: docker compose down · y/n") {
		t.Errorf("force-remove prompt = %q", f)
	}
}
