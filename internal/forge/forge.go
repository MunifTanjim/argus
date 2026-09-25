// Package forge reads pull requests and issues from the code host behind a
// repository's origin remote.
package forge

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/MunifTanjim/argus/internal/shell"
)

// Provider is a code host. Its lists return open items only.
type Provider interface {
	ListPRs(ctx context.Context, repoDir string) ([]PullRequest, error)
	GetPR(ctx context.Context, repoDir string, number int) (PullRequest, error)
	ListIssues(ctx context.Context, repoDir string) ([]Issue, error)
	GetIssue(ctx context.Context, repoDir string, number int) (Issue, error)
	CheckoutPR(ctx context.Context, worktreeDir string, number int, branch string) error
}

type PullRequest struct {
	Number     int
	Title      string
	Author     string
	HeadBranch string
	BaseBranch string
	URL        string
}

type Issue struct {
	Number int
	Title  string
	Body   string // set by GetIssue only
	Author string
	URL    string
}

// ListLimit caps ListPRs and ListIssues.
const ListLimit = 100

var ErrNoProvider = errors.New("PRs and issues need a github.com origin remote")

// For picks the provider for repoDir's origin remote.
func For(ctx context.Context, repoDir string) (Provider, error) {
	cmd := shell.NewCommandContext(ctx, "git", "-C", repoDir, "remote", "get-url", "origin")
	if err := cmd.Run(); err != nil {
		return nil, ErrNoProvider
	}
	if remoteHost(cmd.StdOut().TrimSpace().String()) == "github.com" {
		return github{}, nil
	}
	return nil, ErrNoProvider
}

// remoteHost extracts the host from an HTTPS/SSH URL or scp-like "git@host:path".
func remoteHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Hostname()
	}
	if at := strings.Index(raw, "@"); at >= 0 {
		raw = raw[at+1:]
	}
	host, _, _ := strings.Cut(raw, ":")
	return host
}

// Slug turns an issue title into a branch-name fragment.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}
