package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/node"
)

func TestPrintProjectTable(t *testing.T) {
	out := captureStdout(t, func() {
		printProjectTable(api.ProjectListResult{Projects: []api.ProjectNode{
			{ID: "aaaaaa111111ffff", Name: "argus", Kind: "git", Dir: "/repo/.git", Root: "/repo", DefaultBranch: "main", Hidden: true, Pinned: true, Workspaces: []api.WorkspaceNode{{}, {}, {}}},
			{ID: "bbbbbb222222ffff", Name: "bare", Kind: "git", Dir: "/bare.git", Error: "worktree list: git missing"},
		}})
	})
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	want := []string{
		"ID            NAME   KIND  DEFAULT  WORKSPACES  STATUS                                 ROOT",
		"aaaaaa111111  argus  git   main     3           pinned, hidden                         /repo",
		"bbbbbb222222  bare   git   -        0           git error: worktree list: git missing  /bare.git",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("table =\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestPrintWorkspaceTableAlignsWideRunes(t *testing.T) {
	out := captureStdout(t, func() {
		printWorkspaceTable([]api.ProjectNode{{Name: "argus", Kind: "git", Workspaces: []api.WorkspaceNode{
			{ID: "wwwwww111111ffff", Dir: "/repo", IsMain: true, Branch: "main", TargetBranch: "main", Head: "ab24ff4"},
			{ID: "wwwwww222222ffff", Dir: "/repo-ü", Branch: "fix-ümlaut", TargetBranch: "main", Head: "75bd1e0"},
			{ID: "wwwwww333333ffff", Dir: "/repo-old", IsGone: true},
		}}}, true)
	})
	want := strings.Join([]string{
		"ID            PROJECT  BRANCH      TARGET  HEAD     STATUS  DIR",
		"wwwwww111111  argus    main        main    ab24ff4  main    /repo",
		"wwwwww222222  argus    fix-ümlaut  main    75bd1e0  -       /repo-ü",
		"wwwwww333333  argus    -           -       -        gone    /repo-old",
	}, "\n") + "\n"
	if out != want {
		t.Errorf("table =\n%s\nwant\n%s", out, want)
	}
}

func projectFixture() api.ProjectListResult {
	return api.ProjectListResult{Projects: []api.ProjectNode{
		{ID: "aaaaaa111111ffff", Name: "argus", Kind: "git", Root: "/repo", Workspaces: []api.WorkspaceNode{
			{ID: "wwwwww111111ffff", Dir: "/repo", IsMain: true, Branch: "main"},
			{ID: "wwwwww222222ffff", Dir: "/repo/.worktrees/feat", Branch: "feat"},
		}},
		{ID: "bbbbbb222222ffff", Name: "twin", Kind: "plain", Root: "/twin-a", Workspaces: []api.WorkspaceNode{
			{ID: "wwwwww555555ffff", Dir: "/twin-a", IsMain: true},
		}},
		{ID: "cccccc333333ffff", Name: "twin", Kind: "plain", Root: "/twin-b", Workspaces: []api.WorkspaceNode{
			{ID: "wwwwww666666ffff", Dir: "/twin-b", IsMain: true},
		}},
	}}
}

func TestResolveProject(t *testing.T) {
	list := projectFixture()
	for arg, want := range map[string]string{
		"argus":                   "aaaaaa111111ffff",
		"aaaaaa111111ffff":        "aaaaaa111111ffff",
		"bbbbbb":                  "bbbbbb222222ffff",
		"/repo/.worktrees/feat/x": "aaaaaa111111ffff", // a path inside any of its workspaces
		"/twin-b":                 "cccccc333333ffff",
	} {
		if p, err := resolveProject(list, arg); err != nil || p.ID != want {
			t.Errorf("resolveProject(%q) = %q, %v; want %q", arg, p.ID, err, want)
		}
	}
	for _, arg := range []string{"twin", "nope", "aaa", "/elsewhere"} {
		if _, err := resolveProject(list, arg); err == nil {
			t.Errorf("resolveProject(%q) should fail", arg)
		}
	}
}

func TestResolveWorkspace(t *testing.T) {
	list := projectFixture()
	for arg, want := range map[string]string{
		"wwwwww222222":                     "wwwwww222222ffff",
		"/repo/.worktrees/feat/internal/x": "wwwwww222222ffff", // a path inside picks the innermost workspace
		"/repo/cmd":                        "wwwwww111111ffff",
	} {
		if w, err := resolveWorkspace(list, arg); err != nil || w.ID != want {
			t.Errorf("resolveWorkspace(%q) = %q, %v; want %q", arg, w.ID, err, want)
		}
	}
	if _, err := resolveWorkspace(list, "/elsewhere"); err == nil {
		t.Error("a path outside every workspace should fail")
	}
}

type fakeNode struct {
	list      api.ProjectListResult
	calls     []string
	params    map[string]any
	lists     []api.ProjectListResult // served in order after list, for --wait polls
	removeRes api.WorkspaceRemoveResult
	createRes *api.WorkspaceCreateResult // nil serves the default result
	log       string
}

func (f *fakeNode) call(method string, params, out any) error {
	f.calls = append(f.calls, method)
	if f.params == nil {
		f.params = map[string]any{}
	}
	f.params[method] = params
	if method == api.MethodProjectList {
		l := f.list
		if n := len(f.calls); n > 1 && len(f.lists) > 0 {
			l, f.lists = f.lists[0], f.lists[1:]
		}
		*out.(*api.ProjectListResult) = l
	}
	if method == api.MethodWorkspaceCreate {
		res := api.WorkspaceCreateResult{WorkspaceID: "wwwwww333333ffff", Dir: "/repo/.worktrees/new", Warning: "fetch failed", Setup: "pnpm install", SetupRun: &api.ScriptRun{State: "running", Command: "pnpm install"}}
		if f.createRes != nil {
			res = *f.createRes
		}
		*out.(*api.WorkspaceCreateResult) = res
	}
	if method == api.MethodWorkspaceRemove && out != nil {
		*out.(*api.WorkspaceRemoveResult) = f.removeRes
	}
	if method == api.MethodWorkspaceSetupLog {
		*out.(*api.SetupLogResult) = api.SetupLogResult{Output: f.log}
	}
	return nil
}

func TestProjectAndWorkspaceCommandsCallTheNode(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		method string
		params any
		out    string
	}{
		{[]string{"project", "rename", "argus", "Argus"}, api.MethodProjectRename, api.ProjectRenameParams{ProjectID: "aaaaaa111111ffff", Name: "Argus"}, "renamed project aaaaaa111111\n  from  argus\n  to    Argus\n"},
		{[]string{"project", "unhide", "argus"}, api.MethodProjectSetHidden, api.ProjectFlagParams{ProjectID: "aaaaaa111111ffff", Value: false}, "unhid project aaaaaa111111\n  name  argus\n"},
		{[]string{"project", "pin", "argus"}, api.MethodProjectSetPinned, api.ProjectFlagParams{ProjectID: "aaaaaa111111ffff", Value: true}, "pinned project aaaaaa111111"},
		{[]string{"project", "forget", "argus"}, api.MethodProjectForget, api.ProjectRef{ProjectID: "aaaaaa111111ffff"}, "forgot project aaaaaa111111"},
		{[]string{"workspace", "create", "argus", "42", "--source", "issue"}, api.MethodWorkspaceCreate, api.WorkspaceCreateParams{ProjectID: "aaaaaa111111ffff", Source: api.SourceIssue, Number: 42}, "created workspace wwwwww333333\n  project  argus\n  dir      /repo/.worktrees/new\n  setup    pnpm install (running)\n"},
		{[]string{"workspace", "create", "argus", "login", "--target", "dev"}, api.MethodWorkspaceCreate, api.WorkspaceCreateParams{ProjectID: "aaaaaa111111ffff", Source: api.SourceNew, Branch: "login", TargetBranch: "dev"}, "created workspace"},
		{[]string{"workspace", "create", "/repo/cmd", "login"}, api.MethodWorkspaceCreate, api.WorkspaceCreateParams{ProjectID: "aaaaaa111111ffff", Source: api.SourceNew, Branch: "login"}, "created workspace"},
		{[]string{"workspace", "remove", "/repo/.worktrees/feat", "--force"}, api.MethodWorkspaceRemove, api.WorkspaceRemoveParams{WorkspaceID: "wwwwww222222ffff", Force: true}, "removed workspace wwwwww222222\n  dir     /repo/.worktrees/feat\n  branch  feat\n"},
		{[]string{"workspace", "target", "wwwwww222222", "dev"}, api.MethodWorkspaceSetTarget, api.WorkspaceSetTargetParams{WorkspaceID: "wwwwww222222ffff", TargetBranch: "dev"}, "set target of workspace wwwwww222222\n  dir     /repo/.worktrees/feat\n  branch  feat\n  target  dev\n"},
	} {
		f := &fakeNode{list: projectFixture()}
		out := captureStdout(t, func() {
			if err := runRegistryCommand(f.call, tc.args); err != nil {
				t.Errorf("%v: %v", tc.args, err)
			}
		})
		if !slices.Contains(f.calls, tc.method) {
			t.Errorf("%v: calls = %v, want %s", tc.args, f.calls, tc.method)
			continue
		}
		if fmt.Sprint(f.params[tc.method]) != fmt.Sprint(tc.params) {
			t.Errorf("%v: params = %+v, want %+v", tc.args, f.params[tc.method], tc.params)
		}
		if !strings.Contains(out, tc.out) {
			t.Errorf("%v: output %q lacks %q", tc.args, out, tc.out)
		}
	}
}

func runRegistryCommand(call caller, args []string) error {
	dial := func(*cobra.Command) (caller, func(), error) { return call, func() {}, nil }
	root := &cobra.Command{Use: "argus"}
	root.AddCommand(projectCmd(dial), workspaceCmd(dial))
	root.SetArgs(args)
	return root.Execute()
}

func listWithSetup(state string, code int, tail string) api.ProjectListResult {
	l := projectFixture()
	l.Projects[0].Workspaces = append(l.Projects[0].Workspaces, api.WorkspaceNode{
		ID: "wwwwww333333ffff", Dir: "/repo/.worktrees/new",
		Setup: &api.ScriptRun{State: state, Command: "pnpm install", ExitCode: code, OutputTail: tail},
	})
	return l
}

func fastSetupPoll(t *testing.T) {
	old := setupPoll
	setupPoll = time.Millisecond
	t.Cleanup(func() { setupPoll = old })
}

func TestCreateWaitsForSetup(t *testing.T) {
	fastSetupPoll(t)
	f := &fakeNode{list: projectFixture(), lists: []api.ProjectListResult{listWithSetup("running", 0, ""), listWithSetup("ok", 0, "")}}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "create", "argus", "login", "--wait"}); err != nil {
			t.Errorf("create --wait: %v", err)
		}
	})
	if !strings.Contains(out, "setup    pnpm install (running)") || !strings.Contains(out, "setup done") {
		t.Errorf("output = %q", out)
	}
}

func TestCreateWaitReportsSetupFailure(t *testing.T) {
	fastSetupPoll(t)
	f := &fakeNode{list: projectFixture(), lists: []api.ProjectListResult{listWithSetup("failed", 1, "ERR_PNPM_NO_LOCKFILE")}}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "create", "argus", "login", "--wait"}); err == nil {
			t.Error("a failed setup should make --wait fail")
		}
	})
	if !strings.Contains(out, "ERR_PNPM_NO_LOCKFILE") {
		t.Errorf("output should carry the tail: %q", out)
	}
}

// A setup that fails before it starts (a bad settings file) leaves create with
// no setup command, but --wait must still report the failure.
func TestCreateWaitReportsASetupThatFailedToStart(t *testing.T) {
	fastSetupPoll(t)
	f := &fakeNode{
		list:      projectFixture(),
		createRes: &api.WorkspaceCreateResult{WorkspaceID: "wwwwww333333ffff", Dir: "/repo/.worktrees/new", SetupRun: &api.ScriptRun{State: "failed", ExitCode: -1, OutputTail: ".argus/settings.toml: bad"}},
	}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "create", "argus", "login", "--wait"}); err == nil {
			t.Error("a setup that failed to start should make --wait fail")
		}
	})
	if !strings.Contains(out, ".argus/settings.toml: bad") {
		t.Errorf("output should carry the error: %q", out)
	}
}

func TestCreateWaitWithoutSetupIsQuiet(t *testing.T) {
	fastSetupPoll(t)
	f := &fakeNode{list: projectFixture(), createRes: &api.WorkspaceCreateResult{WorkspaceID: "wwwwww333333ffff", Dir: "/repo/.worktrees/new"}}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "create", "argus", "login", "--wait"}); err != nil {
			t.Errorf("create --wait with no setup: %v", err)
		}
	})
	if strings.Contains(out, "setup") {
		t.Errorf("no setup should print nothing about setup: %q", out)
	}
}

func TestWaitReportsALostSetup(t *testing.T) {
	fastSetupPoll(t)
	f := &fakeNode{lists: []api.ProjectListResult{listWithSetup("running", 0, ""), projectFixture()}}
	f.calls = []string{"seed"} // serve lists from the first poll on
	_, err := waitSetup(f.call, "wwwwww333333ffff", &api.ScriptRun{State: "running"})
	if err == nil || err.Error() != "setup state lost (node restarted or workspace removed)" {
		t.Errorf("a setup that disappears mid-wait: %v", err)
	}
}

func TestSetupAndSetupLogCommands(t *testing.T) {
	f := &fakeNode{list: projectFixture(), log: "installing\ndone\n"}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "setup", "wwwwww222222"}); err != nil {
			t.Error(err)
		}
		if err := runRegistryCommand(f.call, []string{"workspace", "setup-log", "wwwwww222222"}); err != nil {
			t.Error(err)
		}
	})
	if !slices.Contains(f.calls, api.MethodWorkspaceRunSetup) || !strings.Contains(out, "installing") {
		t.Errorf("calls=%v output=%q", f.calls, out)
	}
}

func TestRemovePrintsTeardownWarning(t *testing.T) {
	f := &fakeNode{list: projectFixture(), removeRes: api.WorkspaceRemoveResult{Warning: "teardown failed (exit 4): db"}}
	out, errOut := captureOutput(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "remove", "wwwwww222222", "--force"}); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(out, "warning") || !strings.Contains(errOut, "warning: teardown failed (exit 4): db") {
		t.Errorf("stdout = %q, stderr = %q", out, errOut)
	}
}

func TestListMarksSetupState(t *testing.T) {
	out := captureStdout(t, func() { printWorkspaceTable(listWithSetup("running", 0, "").Projects, true) })
	if !strings.Contains(out, "setting up") {
		t.Errorf("output = %q", out)
	}
	out = captureStdout(t, func() { printWorkspaceTable(listWithSetup("failed", 1, "").Projects, true) })
	if !strings.Contains(out, "setup failed") {
		t.Errorf("output = %q", out)
	}
}

// A run that never exited, or a failed file copy, has no exit code to show.
func TestSetupErrWithoutExitCode(t *testing.T) {
	for _, run := range []api.ScriptRun{
		{State: "failed", ExitCode: -1, OutputTail: "timed out after 15m"},
		{State: "failed", ExitCode: 0, OutputTail: ".worktreeinclude: copied 1 of 2 files"},
	} {
		if err := setupErr(&run); err == nil || err.Error() != "setup failed" {
			t.Errorf("setupErr(%+v) = %v", run, err)
		}
	}
}

func TestEnableProjectRegistryRejectsRelativeAutoAdoptDir(t *testing.T) {
	orig := config.DataDir
	config.DataDir = t.TempDir()
	t.Cleanup(func() { config.DataDir = orig })

	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoAdoptDirs: []string{"Dev"}}}
	err := enableProjectRegistry(node.New(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "workspace.auto-adopt-dirs") {
		t.Fatalf("err = %v, want one naming workspace.auto-adopt-dirs", err)
	}
}

func TestWorkspaceListJSONIsFlat(t *testing.T) {
	f := &fakeNode{list: projectFixture()}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "list", "--json"}); err != nil {
			t.Error(err)
		}
	})
	var got []workspaceEntry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 4 || got[1].ID != "wwwwww222222ffff" || got[1].ProjectID != "aaaaaa111111ffff" || got[1].ProjectName != "argus" || got[1].Branch != "feat" {
		t.Errorf("entries = %+v", got)
	}
}

func TestWorkspaceListOfOneProjectDropsTheProjectColumn(t *testing.T) {
	f := &fakeNode{list: projectFixture()}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "list", "argus"}); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(out, "PROJECT") || strings.Contains(out, "twin") || !strings.Contains(out, "wwwwww222222") {
		t.Errorf("output =\n%s", out)
	}
}

func TestCreateWaitJSONPrintsOnlyTheResult(t *testing.T) {
	fastSetupPoll(t)
	for _, tc := range []struct {
		final   api.ProjectListResult
		state   string
		wantErr bool
	}{
		{listWithSetup("ok", 0, ""), "ok", false},
		{listWithSetup("failed", 2, "boom"), "failed", true},
	} {
		f := &fakeNode{list: projectFixture(), lists: []api.ProjectListResult{listWithSetup("running", 0, ""), tc.final}}
		out, errOut := captureOutput(t, func() {
			err := runRegistryCommand(f.call, []string{"workspace", "create", "argus", "login", "--wait", "--json"})
			if (err != nil) != tc.wantErr {
				t.Errorf("%s: err = %v", tc.state, err)
			}
		})
		var got workspaceResult
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%s: stdout is not one JSON value: %v\n%s", tc.state, err, out)
		}
		if got.WorkspaceID != "wwwwww333333ffff" || got.Warning != "fetch failed" || got.Setup == nil || got.Setup.State != tc.state {
			t.Errorf("%s: result = %+v", tc.state, got)
		}
		if strings.Contains(errOut, "warning") {
			t.Errorf("%s: the warning belongs in the JSON, not stderr: %q", tc.state, errOut)
		}
	}
}

func TestProjectFlagJSON(t *testing.T) {
	f := &fakeNode{list: projectFixture()}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"project", "hide", "argus", "--json"}); err != nil {
			t.Error(err)
		}
	})
	want := "{\n  \"project_id\": \"aaaaaa111111ffff\",\n  \"name\": \"argus\",\n  \"hidden\": true\n}\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestCreateWaitJSONPrintsTheResultWhenTheWaitFails(t *testing.T) {
	fastSetupPoll(t)
	f := &fakeNode{list: projectFixture(), lists: []api.ProjectListResult{projectFixture()}} // the setup vanishes
	out, errOut := captureOutput(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "create", "argus", "login", "--wait", "--json"}); err == nil {
			t.Error("a lost setup should make --wait fail")
		}
	})
	if !strings.Contains(errOut, "setup state lost") {
		t.Errorf("stderr = %q", errOut)
	}
	var got workspaceResult
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.WorkspaceID != "wwwwww333333ffff" {
		t.Errorf("stdout should still carry the created workspace: %v\n%s", err, out)
	}
}

// A node older than the CLI reports no setup run; --wait must still find it.
func TestCreateWaitFindsASetupTheNodeDidNotReport(t *testing.T) {
	fastSetupPoll(t)
	f := &fakeNode{
		list:      projectFixture(),
		createRes: &api.WorkspaceCreateResult{WorkspaceID: "wwwwww333333ffff", Dir: "/repo/.worktrees/new", Setup: "pnpm install"},
		lists:     []api.ProjectListResult{listWithSetup("running", 0, ""), listWithSetup("failed", 3, "")},
	}
	_, errOut := captureOutput(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "create", "argus", "login", "--wait"}); err == nil {
			t.Error("a failed setup should make --wait fail")
		}
	})
	if !strings.Contains(errOut, "setup failed (exit 3)") {
		t.Errorf("stderr = %q", errOut)
	}
}

// A reset target reports the default branch it resolves to, as workspace list does.
func TestTargetResetReportsTheDefaultBranch(t *testing.T) {
	list := projectFixture()
	list.Projects[0].DefaultBranch = "main"
	f := &fakeNode{list: list}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "target", "wwwwww222222", "", "--json"}); err != nil {
			t.Error(err)
		}
		if err := runRegistryCommand(f.call, []string{"workspace", "target", "wwwwww222222", ""}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, `"target_branch": "main"`) || !strings.Contains(out, "target  main (default branch)\n") {
		t.Errorf("output =\n%s", out)
	}
}
