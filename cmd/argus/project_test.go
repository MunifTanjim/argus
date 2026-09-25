package main

import (
	"fmt"
	"strings"
	"testing"

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
	list  api.ProjectListResult
	calls []string
	last  any
}

func (f *fakeNode) call(method string, params, out any) error {
	f.calls = append(f.calls, method)
	f.last = params
	if method == api.MethodProjectList {
		*out.(*api.ProjectListResult) = f.list
	}
	if method == api.MethodWorkspaceCreate {
		*out.(*api.WorkspaceCreateResult) = api.WorkspaceCreateResult{Dir: "/repo/.worktrees/new", Warning: "fetch failed"}
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
