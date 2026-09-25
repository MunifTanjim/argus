package main

import (
	"strings"
	"testing"

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
