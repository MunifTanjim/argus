package forge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGH puts a `gh` script on PATH that prints canned JSON by subcommand.
func fakeGH(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGitHubParsesGHOutput(t *testing.T) {
	fakeGH(t, `case "$1 $2" in
"pr list") echo '[{"number":7,"title":"Fix","author":{"login":"al"},"headRefName":"fix","baseRefName":"main","url":"u7"}]';;
"pr view") echo '{"number":7,"title":"Fix","author":{"login":"al"},"headRefName":"fix","baseRefName":"dev","url":"u7"}';;
"issue list") echo '[{"number":3,"title":"Bug","author":{"login":"bo"},"url":"u3"}]';;
"issue view") echo '{"number":3,"title":"Bug","body":"steps","author":{"login":"bo"},"url":"u3"}';;
esac`)
	ctx := context.Background()
	g := github{}
	prs, err := g.ListPRs(ctx, t.TempDir())
	if err != nil || len(prs) != 1 || prs[0] != (PullRequest{Number: 7, Title: "Fix", Author: "al", HeadBranch: "fix", BaseBranch: "main", URL: "u7"}) {
		t.Fatalf("ListPRs = %+v, %v", prs, err)
	}
	pr, err := g.GetPR(ctx, t.TempDir(), 7)
	if err != nil || pr.BaseBranch != "dev" {
		t.Fatalf("GetPR = %+v, %v", pr, err)
	}
	is, err := g.ListIssues(ctx, t.TempDir())
	if err != nil || len(is) != 1 || is[0].Author != "bo" {
		t.Fatalf("ListIssues = %+v, %v", is, err)
	}
	i, err := g.GetIssue(ctx, t.TempDir(), 3)
	if err != nil || i.Body != "steps" {
		t.Fatalf("GetIssue = %+v, %v", i, err)
	}
}

func TestGitHubMissingGHReturnsError(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no gh anywhere
	if _, err := (github{}).ListPRs(context.Background(), t.TempDir()); err == nil || err.Error() != "GitHub CLI (gh) is not installed" {
		t.Fatalf("err = %v, want a plain not-installed message", err)
	}
}

func TestGitHubLoggedOutSaysHowToLogIn(t *testing.T) {
	fakeGH(t, `printf 'To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable.\n' >&2; exit 4`)
	_, err := (github{}).ListIssues(context.Background(), t.TempDir())
	if err == nil || err.Error() != "GitHub CLI is not logged in; run gh auth login" {
		t.Fatalf("err = %v, want the log-in hint", err)
	}
}

func TestGitHubErrorKeepsFirstLine(t *testing.T) {
	fakeGH(t, `printf 'HTTP 502: bad gateway\nretry later\n' >&2; exit 1`)
	_, err := (github{}).ListIssues(context.Background(), t.TempDir())
	if err == nil || err.Error() != "gh: HTTP 502: bad gateway" {
		t.Fatalf("err = %v, want gh's first line", err)
	}
}

func TestGitHubTimeoutNamesTheContextThatExpired(t *testing.T) {
	fakeGH(t, `sleep 5`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := (github{}).ListIssues(ctx, t.TempDir())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errGHTimeout) {
		t.Errorf("outer deadline: err = %v, want the caller's deadline", err)
	}

	inner, cancel := context.WithTimeoutCause(context.Background(), 0, errGHTimeout)
	defer cancel()
	if err := ghError(inner, errors.New("killed"), ""); !errors.Is(err, errGHTimeout) {
		t.Errorf("gh deadline: err = %v, want %v", err, errGHTimeout)
	}
}

func TestNoProviderMessage(t *testing.T) {
	if ErrNoProvider.Error() != "PRs and issues need a github.com origin remote" {
		t.Errorf("ErrNoProvider = %q", ErrNoProvider)
	}
}

func TestGitHubErrorCarriesGHMessage(t *testing.T) {
	fakeGH(t, `echo "gh: not logged in" >&2; exit 1`)
	_, err := (github{}).ListIssues(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v, want gh's message", err)
	}
}

func TestForPicksByOriginHost(t *testing.T) {
	ctx := context.Background()
	repo := func(url string) string {
		dir := t.TempDir()
		exec.Command("git", "-C", dir, "init").Run()
		if url != "" {
			exec.Command("git", "-C", dir, "remote", "add", "origin", url).Run()
		}
		return dir
	}
	for _, url := range []string{"https://github.com/o/r.git", "git@github.com:o/r.git", "ssh://git@github.com/o/r"} {
		if _, err := For(ctx, repo(url)); err != nil {
			t.Errorf("For(%s) = %v, want the GitHub provider", url, err)
		}
	}
	for _, url := range []string{"https://gitlab.com/o/r.git", ""} {
		if _, err := For(ctx, repo(url)); !errors.Is(err, ErrNoProvider) {
			t.Errorf("For(%q) = %v, want ErrNoProvider", url, err)
		}
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Fix login crash on resume!": "fix-login-crash-on-resume",
		"  --Hello__World--  ":       "hello-world",
		"修复":                         "",
		strings.Repeat("abc ", 20):   "abc-abc-abc-abc-abc-abc-abc-abc-abc-abc",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
