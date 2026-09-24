package forge

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/MunifTanjim/argus/internal/shell"
)

const ghTimeout = 20 * time.Second

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
	if err := gh(ctx, repoDir, &raw, "pr", "list", "--state", "open", "--limit", "100", "--json", prFields); err != nil {
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
	if err := gh(ctx, repoDir, &raw, "issue", "list", "--state", "open", "--limit", "100", "--json", "number,title,author,url"); err != nil {
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
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()
	cmd := shell.NewCommandContext(ctx, "gh", args...).WithDir(dir)
	if err := cmd.Run(); err != nil {
		if msg := cmd.StdErr().TrimSpace().String(); msg != "" {
			return fmt.Errorf("gh: %s", msg)
		}
		return fmt.Errorf("gh: %w", err)
	}
	if out == nil {
		return nil
	}
	return cmd.StdOut().JSONUnmarshal(out)
}
