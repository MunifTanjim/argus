package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestPrintProjectTree(t *testing.T) {
	out := captureStdout(t, func() {
		printProjectTree(api.ProjectListResult{Projects: []api.ProjectNode{
			{Name: "argus", Kind: "git", Dir: "/repo/.git", Root: "/repo", Hidden: true, Workspaces: []api.WorkspaceNode{
				{Dir: "/repo", IsMain: true, Branch: "main", TargetBranch: "main"},
				{Dir: "/repo-feat", Branch: "feature", TargetBranch: "main"},
				{Dir: "/repo-old", IsGone: true, TargetBranch: "main"},
			}},
			{Name: "notes", Kind: "plain", Dir: "/notes", Root: "/notes", Workspaces: []api.WorkspaceNode{{Dir: "/notes", IsMain: true}}},
		}})
	})
	for _, want := range []string{"argus (hidden)", "\n  /repo\n", "feature → main", "/repo-old (gone)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"/repo/.git", "(detached)"} {
		if strings.Contains(out, bad) {
			t.Errorf("output has %q:\n%s", bad, out)
		}
	}
}

func TestPrintProjectTreeShowsGitError(t *testing.T) {
	out := captureStdout(t, func() {
		printProjectTree(api.ProjectListResult{Projects: []api.ProjectNode{{Name: "argus", Kind: "git", Root: "/repo", Error: "worktree list: git missing"}}})
	})
	if !strings.Contains(out, "argus (git error: worktree list: git missing)") {
		t.Errorf("output lacks the git error:\n%s", out)
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
	last      any
	lists     []api.ProjectListResult // served in order after list, for --wait polls
	removeRes api.WorkspaceRemoveResult
	createRes *api.WorkspaceCreateResult // nil serves the default result
	log       string
}

func (f *fakeNode) call(method string, params, out any) error {
	f.calls = append(f.calls, method)
	f.last = params
	if method == api.MethodProjectList {
		l := f.list
		if n := len(f.calls); n > 1 && len(f.lists) > 0 {
			l, f.lists = f.lists[0], f.lists[1:]
		}
		*out.(*api.ProjectListResult) = l
	}
	if method == api.MethodWorkspaceCreate {
		res := api.WorkspaceCreateResult{WorkspaceID: "wwwwww333333ffff", Dir: "/repo/.worktrees/new", Warning: "fetch failed", Setup: "pnpm install"}
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
		{[]string{"project", "rename", "argus", "Argus"}, api.MethodProjectRename, api.ProjectRenameParams{ProjectID: "aaaaaa111111ffff", Name: "Argus"}, "renamed argus to Argus"},
		{[]string{"project", "unhide", "argus"}, api.MethodProjectSetHidden, api.ProjectFlagParams{ProjectID: "aaaaaa111111ffff", Value: false}, "unhid argus"},
		{[]string{"project", "pin", "argus"}, api.MethodProjectSetPinned, api.ProjectFlagParams{ProjectID: "aaaaaa111111ffff", Value: true}, "pinned argus"},
		{[]string{"project", "forget", "argus"}, api.MethodProjectForget, api.ProjectRef{ProjectID: "aaaaaa111111ffff"}, "forgot argus"},
		{[]string{"workspace", "create", "argus", "42", "--source", "issue"}, api.MethodWorkspaceCreate, api.WorkspaceCreateParams{ProjectID: "aaaaaa111111ffff", Source: api.SourceIssue, Number: 42}, "created workspace /repo/.worktrees/new\nwarning: fetch failed"},
		{[]string{"workspace", "create", "argus", "login", "--target", "dev"}, api.MethodWorkspaceCreate, api.WorkspaceCreateParams{ProjectID: "aaaaaa111111ffff", Source: api.SourceNew, Branch: "login", TargetBranch: "dev"}, "created workspace"},
		{[]string{"workspace", "create", "/repo/cmd", "login"}, api.MethodWorkspaceCreate, api.WorkspaceCreateParams{ProjectID: "aaaaaa111111ffff", Source: api.SourceNew, Branch: "login"}, "created workspace"},
		{[]string{"workspace", "remove", "/repo/.worktrees/feat", "--force"}, api.MethodWorkspaceRemove, api.WorkspaceRemoveParams{WorkspaceID: "wwwwww222222ffff", Force: true}, "removed /repo/.worktrees/feat"},
		{[]string{"workspace", "target", "wwwwww222222", "dev"}, api.MethodWorkspaceSetTarget, api.WorkspaceSetTargetParams{WorkspaceID: "wwwwww222222ffff", TargetBranch: "dev"}, "target of /repo/.worktrees/feat → dev"},
	} {
		f := &fakeNode{list: projectFixture()}
		out := captureStdout(t, func() {
			if err := runRegistryCommand(f.call, tc.args); err != nil {
				t.Errorf("%v: %v", tc.args, err)
			}
		})
		if len(f.calls) == 0 || f.calls[len(f.calls)-1] != tc.method {
			t.Errorf("%v: calls = %v, want %s last", tc.args, f.calls, tc.method)
			continue
		}
		if fmt.Sprint(f.last) != fmt.Sprint(tc.params) {
			t.Errorf("%v: params = %+v, want %+v", tc.args, f.last, tc.params)
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
	if !strings.Contains(out, "setup started: pnpm install") || !strings.Contains(out, "setup done") {
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
		createRes: &api.WorkspaceCreateResult{WorkspaceID: "wwwwww333333ffff", Dir: "/repo/.worktrees/new"},
		lists:     []api.ProjectListResult{listWithSetup("failed", -1, ".argus/settings.toml: bad")},
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
	out := captureStdout(t, func() {
		err := waitSetup(f.call, "wwwwww333333ffff")
		if err == nil || err.Error() != "setup state lost (node restarted or workspace removed)" {
			t.Errorf("a setup that disappears mid-wait: %v", err)
		}
	})
	if strings.Contains(out, "setup done") {
		t.Errorf("a lost setup is not done: %q", out)
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
	if !contains(f.calls, api.MethodWorkspaceRunSetup) || !strings.Contains(out, "installing") {
		t.Errorf("calls=%v output=%q", f.calls, out)
	}
}

func TestRemovePrintsTeardownWarning(t *testing.T) {
	f := &fakeNode{list: projectFixture(), removeRes: api.WorkspaceRemoveResult{Warning: "teardown failed (exit 4): db"}}
	out := captureStdout(t, func() {
		if err := runRegistryCommand(f.call, []string{"workspace", "remove", "wwwwww222222", "--force"}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "warning: teardown failed (exit 4): db") {
		t.Errorf("output = %q", out)
	}
}

func TestListMarksSetupState(t *testing.T) {
	out := captureStdout(t, func() { printProjectTree(listWithSetup("running", 0, "")) })
	if !strings.Contains(out, "(setting up)") {
		t.Errorf("output = %q", out)
	}
	out = captureStdout(t, func() { printProjectTree(listWithSetup("failed", 1, "")) })
	if !strings.Contains(out, "(setup failed)") {
		t.Errorf("output = %q", out)
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

func TestWaitReportsASetupWithNoExitCode(t *testing.T) {
	f := &fakeNode{lists: []api.ProjectListResult{listWithSetup("failed", -1, "timed out after 15m")}}
	f.calls = []string{"seed"}
	captureStdout(t, func() {
		if err := waitSetup(f.call, "wwwwww333333ffff"); err == nil || err.Error() != "setup failed" {
			t.Errorf("a setup that never exited has no exit code to show: %v", err)
		}
	})
}
