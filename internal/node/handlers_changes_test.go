package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// nodeNoDirSession registers a session whose working directory is unknown (no
// hook Cwd, no tmux pane path), so sessionDir() returns "".
func nodeNoDirSession(t *testing.T) (*Node, string) {
	t.Helper()
	d := newTestNode(t)
	d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{{
		AgentSessionID: "s1", Frontend: session.FrontendTmux,
	}})
	var id string
	for _, s := range d.reg.Snapshot() {
		id = s.ID
	}
	if id == "" {
		t.Fatal("session not registered")
	}
	return d, id
}

// rpcCode returns the RPCError code from err, or 0 if err is not an *api.RPCError.
func rpcCode(err error) int {
	var re *api.RPCError
	if errors.As(err, &re) {
		return re.Code
	}
	return 0
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func nodeWithGitSession(t *testing.T) (*Node, string, string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "base")
	runGit(t, dir, "checkout", "-b", "feat")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-am", "second")

	d := newTestNode(t)
	d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{{
		AgentSessionID: "s1", Cwd: dir, Frontend: session.FrontendTmux,
	}})
	var id string
	for _, s := range d.reg.Snapshot() {
		id = s.ID
	}
	if id == "" {
		t.Fatal("session not registered")
	}
	return d, id, dir
}

func TestHandleCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	d, id, _ := nodeWithGitSession(t)
	params, _ := json.Marshal(api.SessionRef{SessionID: id})
	raw, err := d.handleCommits(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	res := raw.(api.CommitsResult)
	if len(res.Commits) != 1 || res.Commits[0].Subject != "second" {
		t.Fatalf("commits = %+v", res.Commits)
	}
	if res.Commits[0].Short == "" || res.Commits[0].SHA == "" {
		t.Errorf("commit missing sha fields: %+v", res.Commits[0])
	}
}

func TestHandleCommitFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	d, id, dir := nodeWithGitSession(t)
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = dir
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := string(shaOut[:len(shaOut)-1]) // drop trailing newline

	params, _ := json.Marshal(api.CommitFilesParams{SessionID: id, SHA: sha})
	raw, err := d.handleCommitFiles(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	res := raw.(api.ChangedFilesResult)
	if len(res.Files) != 1 || res.Files[0].Path != "a.txt" || res.Files[0].Change != "modified" {
		t.Fatalf("files = %+v", res.Files)
	}
}

func TestHandleChangedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	d, id, dir := nodeWithGitSession(t)
	// An unstaged edit on top of the committed history so status has an entry.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	params, _ := json.Marshal(api.SessionRef{SessionID: id})
	raw, err := d.handleChangedFiles(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	res := raw.(api.ChangedFilesResult)
	if res.Root == "" {
		t.Error("empty root")
	}
	var found bool
	for _, f := range res.Files {
		if f.Path == "a.txt" {
			found = true
			if f.Change != "modified" || f.Staged || !f.Unstaged {
				t.Errorf("a.txt: %+v", f)
			}
		}
	}
	if !found {
		t.Errorf("a.txt missing from status: %+v", res.Files)
	}
}

func TestHandleChangedFilesErrors(t *testing.T) {
	t.Run("unknown session", func(t *testing.T) {
		d := newTestNode(t)
		params, _ := json.Marshal(api.SessionRef{SessionID: "nope"})
		if _, err := d.handleChangedFiles(context.Background(), params); err == nil {
			t.Error("expected error for unknown session")
		}
	})
	t.Run("unknown working directory", func(t *testing.T) {
		d, id := nodeNoDirSession(t)
		params, _ := json.Marshal(api.SessionRef{SessionID: id})
		_, err := d.handleChangedFiles(context.Background(), params)
		if rpcCode(err) != api.CodeInvalidRequest {
			t.Fatalf("err=%v, want InvalidRequest RPCError", err)
		}
	})
}

func TestHandleFileDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	d, id, dir := nodeWithGitSession(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("working tree diff", func(t *testing.T) {
		params, _ := json.Marshal(api.FileDiffParams{SessionID: id, Path: "a.txt"})
		raw, err := d.handleFileDiff(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}
		res := raw.(api.FileDiffResult)
		if res.OldContent != "1\n2\n" || res.NewContent != "1\n2\n3\n" {
			t.Errorf("old=%q new=%q", res.OldContent, res.NewContent)
		}
	})

	t.Run("path required", func(t *testing.T) {
		params, _ := json.Marshal(api.FileDiffParams{SessionID: id})
		_, err := d.handleFileDiff(context.Background(), params)
		if rpcCode(err) != api.CodeInvalidRequest {
			t.Fatalf("err=%v, want InvalidRequest RPCError", err)
		}
	})

	t.Run("unknown session", func(t *testing.T) {
		params, _ := json.Marshal(api.FileDiffParams{SessionID: "nope", Path: "a.txt"})
		if _, err := d.handleFileDiff(context.Background(), params); err == nil {
			t.Error("expected error for unknown session")
		}
	})

	t.Run("path escaping repo is rejected", func(t *testing.T) {
		params, _ := json.Marshal(api.FileDiffParams{SessionID: id, Path: "../escape.txt"})
		_, err := d.handleFileDiff(context.Background(), params)
		if rpcCode(err) != api.CodeInvalidRequest {
			t.Fatalf("err=%v, want InvalidRequest RPCError", err)
		}
	})
}

func TestHandleCommitFilesErrors(t *testing.T) {
	t.Run("sha required", func(t *testing.T) {
		d, id := nodeNoDirSession(t)
		params, _ := json.Marshal(api.CommitFilesParams{SessionID: id})
		_, err := d.handleCommitFiles(context.Background(), params)
		if rpcCode(err) != api.CodeInvalidRequest {
			t.Fatalf("err=%v, want InvalidRequest RPCError", err)
		}
	})

	t.Run("unknown session", func(t *testing.T) {
		d := newTestNode(t)
		params, _ := json.Marshal(api.CommitFilesParams{SessionID: "nope", SHA: "abcdef1"})
		if _, err := d.handleCommitFiles(context.Background(), params); err == nil {
			t.Error("expected error for unknown session")
		}
	})

	// An option-like sha must be rejected and must not write a file.
	t.Run("argument injection blocked", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not available")
		}
		d, id, dir := nodeWithGitSession(t)
		victim := filepath.Join(dir, "PWNED.txt")
		params, _ := json.Marshal(api.CommitFilesParams{SessionID: id, SHA: "--output=" + victim})
		if _, err := d.handleCommitFiles(context.Background(), params); err == nil {
			t.Error("expected error for option-like sha")
		}
		if _, serr := os.Stat(victim); serr == nil {
			t.Fatalf("argument injection wrote %s", victim)
		}
	})
}

func TestSessionDir(t *testing.T) {
	tests := []struct {
		name string
		s    session.Session
		want string
	}{
		{
			name: "cwd wins",
			s:    session.Session{Cwd: "/repo/from/hook", Tmux: session.TmuxLocation{CurrentPath: "/pane/path"}},
			want: "/repo/from/hook",
		},
		{
			name: "falls back to pane path",
			s:    session.Session{Tmux: session.TmuxLocation{CurrentPath: "/pane/path"}},
			want: "/pane/path",
		},
		{
			name: "empty when neither known",
			s:    session.Session{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionDir(tt.s); got != tt.want {
				t.Errorf("sessionDir() = %q, want %q", got, tt.want)
			}
		})
	}
}
