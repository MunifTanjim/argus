package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gitstatus"
	"github.com/MunifTanjim/argus/internal/session"
)

// Changed-files review handlers: report a session working directory's git status
// (vs HEAD) and fetch one changed file's HEAD + working-tree content.

// handleChangedFiles lists every file git status reports for the session's repo.
func (d *Node) handleChangedFiles(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	dir := sessionDir(s)
	if dir == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session working directory unknown"}
	}
	root, files, err := gitstatus.ChangedFiles(ctx, dir)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	out := make([]api.ChangedFile, len(files))
	for i, f := range files {
		out[i] = api.ChangedFile{
			Path:     f.Path,
			OrigPath: f.OrigPath,
			Change:   string(f.Change),
			Staged:   f.Staged,
			Unstaged: f.Unstaged,
		}
	}
	return api.ChangedFilesResult{Root: root, Files: out}, nil
}

// handleFileDiff returns one changed file's HEAD (old) and working-tree (new)
// content for rendering a diff and the full file.
func (d *Node) handleFileDiff(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.FileDiffParams](params)
	if err != nil {
		return nil, err
	}
	if p.Path == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "path is required"}
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	dir := sessionDir(s)
	if dir == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session working directory unknown"}
	}
	var (
		old, nw  string
		notShown bool
	)
	if p.Rev != "" {
		old, nw, notShown, err = gitstatus.CommitFileContents(ctx, dir, p.Rev, p.Path, p.OrigPath)
	} else {
		old, nw, notShown, err = gitstatus.FileContents(ctx, dir, p.Path, p.OrigPath)
	}
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	return api.FileDiffResult{
		Path:       p.Path,
		OldContent: old,
		NewContent: nw,
		NotShown:   notShown,
	}, nil
}

func (d *Node) handleCommits(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	dir := sessionDir(s)
	if dir == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session working directory unknown"}
	}
	log, err := gitstatus.Commits(ctx, dir)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	out := make([]api.Commit, len(log.Commits))
	for i, c := range log.Commits {
		out[i] = api.Commit{
			SHA: c.SHA, Short: c.Short, Subject: c.Subject,
			Author: c.Author, UnixSec: c.UnixSec,
		}
	}
	return api.CommitsResult{Commits: out, Unpushed: log.Unpushed}, nil
}

func (d *Node) handleCommitFiles(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.CommitFilesParams](params)
	if err != nil {
		return nil, err
	}
	if p.SHA == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "sha is required"}
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	dir := sessionDir(s)
	if dir == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session working directory unknown"}
	}
	files, err := gitstatus.CommitFiles(ctx, dir, p.SHA)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	out := make([]api.ChangedFile, len(files))
	for i, f := range files {
		out[i] = api.ChangedFile{
			Path: f.Path, OrigPath: f.OrigPath, Change: string(f.Change), Staged: f.Staged,
		}
	}
	return api.ChangedFilesResult{Files: out}, nil
}

// sessionDir is the working directory to run git in: the hook-reported Cwd when
// known, else the live tmux pane's current path.
func sessionDir(s session.Session) string {
	if s.Cwd != "" {
		return s.Cwd
	}
	return s.Tmux.CurrentPath
}
