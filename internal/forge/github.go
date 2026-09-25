package forge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/shell"
)

const ghTimeout = 20 * time.Second

var errGHTimeout = fmt.Errorf("gh timed out after %s", ghTimeout)

type github struct{}

type ghAuthor struct {
	Login string `json:"login"`
}

type ghPR struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	Author      ghAuthor `json:"author"`
	HeadRefName string   `json:"headRefName"`
	BaseRefName string   `json:"baseRefName"`
	URL         string   `json:"url"`
}

func (p ghPR) toPR() PullRequest {
	return PullRequest{Number: p.Number, Title: p.Title, Author: p.Author.Login, HeadBranch: p.HeadRefName, BaseBranch: p.BaseRefName, URL: p.URL}
}

type ghIssue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Author ghAuthor `json:"author"`
	URL    string   `json:"url"`
}

func (i ghIssue) toIssue() Issue {
	return Issue{Number: i.Number, Title: i.Title, Body: i.Body, Author: i.Author.Login, URL: i.URL}
}

const prFields = "number,title,author,headRefName,baseRefName,url"

func (github) ListPRs(ctx context.Context, repoDir string) ([]PullRequest, error) {
	var raw []ghPR
	if err := gh(ctx, repoDir, &raw, "pr", "list", "--state", "open", "--limit", strconv.Itoa(ListLimit), "--json", prFields); err != nil {
		return nil, err
	}
	out := make([]PullRequest, len(raw))
	for i, p := range raw {
		out[i] = p.toPR()
	}
	return out, nil
}

func (github) GetPR(ctx context.Context, repoDir string, number int) (PullRequest, error) {
	var raw ghPR
	err := gh(ctx, repoDir, &raw, "pr", "view", strconv.Itoa(number), "--json", prFields)
	return raw.toPR(), err
}

func (github) ListIssues(ctx context.Context, repoDir string) ([]Issue, error) {
	var raw []ghIssue
	if err := gh(ctx, repoDir, &raw, "issue", "list", "--state", "open", "--limit", strconv.Itoa(ListLimit), "--json", "number,title,author,url"); err != nil {
		return nil, err
	}
	out := make([]Issue, len(raw))
	for i, is := range raw {
		out[i] = is.toIssue()
	}
	return out, nil
}

func (github) GetIssue(ctx context.Context, repoDir string, number int) (Issue, error) {
	var raw ghIssue
	err := gh(ctx, repoDir, &raw, "issue", "view", strconv.Itoa(number), "--json", "number,title,body,author,url")
	return raw.toIssue(), err
}

func (github) CheckoutPR(ctx context.Context, worktreeDir string, number int, branch string) error {
	return gh(ctx, worktreeDir, nil, "pr", "checkout", strconv.Itoa(number), "--branch", branch)
}

func gh(ctx context.Context, dir string, out any, args ...string) error {
	ctx, cancel := context.WithTimeoutCause(ctx, ghTimeout, errGHTimeout)
	defer cancel()
	cmd := shell.NewCommandContext(ctx, "gh", args...).WithDir(dir)
	if err := cmd.Run(); err != nil {
		return ghError(ctx, err, cmd.StdErr().String())
	}
	if out == nil {
		return nil
	}
	return cmd.StdOut().JSONUnmarshal(out)
}

func ghError(ctx context.Context, err error, stderr string) error {
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return errors.New("GitHub CLI (gh) is not installed")
	case errors.Is(context.Cause(ctx), errGHTimeout):
		return errGHTimeout
	case ctx.Err() != nil:
		return fmt.Errorf("gh stopped: %w", context.Cause(ctx))
	case strings.Contains(stderr, "gh auth login"):
		return errors.New("GitHub CLI is not logged in; run gh auth login")
	}
	for _, ln := range strings.Split(stderr, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			return fmt.Errorf("gh: %s", ln)
		}
	}
	return fmt.Errorf("gh: %w", err)
}
