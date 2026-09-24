// Package projectreg is the node-local registry of Projects and Workspaces,
// persisted in the shared SQLite database and reconciled against the live
// filesystem. Durable structure is stored; volatile git state (branch, head)
// is computed live when the tree is read.
package projectreg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/db/gen"
	"github.com/MunifTanjim/argus/internal/gittree"
)

// Project is one repository (kind "git") or one non-git directory (kind
// "plain"), with its workspaces.
type Project struct {
	ID         string
	Name       string
	Kind       string
	Dir        string
	Root       string // open path: the main workspace's dir ("" for a bare repo)
	IsGone     bool
	Hidden     bool
	Pinned     bool
	CreatedAt  time.Time
	LastSeenAt time.Time
	Workspaces []Workspace
}

// Workspace is one git worktree, or the single directory of a plain project.
// Branch and Head are empty for a gone or plain workspace; Branch is empty for
// a detached HEAD.
type Workspace struct {
	ID         string
	Dir        string
	IsMain     bool
	IsGone     bool
	Branch     string
	Head       string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

type Registry struct {
	db *sql.DB
	q  *gen.Queries
	mu sync.Mutex
}

func New(sqlDB *sql.DB) *Registry {
	return &Registry{db: sqlDB, q: gen.New(sqlDB)}
}

// AdoptSession records the Project and Workspace that contain cwd and returns
// the workspace id. A non-git cwd becomes a plain Project with one workspace.
// An empty cwd, or a cwd inside a bare repo with no working tree, is a no-op.
func (r *Registry) AdoptSession(ctx context.Context, cwd string) (string, error) {
	if cwd == "" {
		return "", nil
	}
	loc, err := gittree.Resolve(ctx, cwd)
	plain := errors.Is(err, gittree.ErrNotRepo)
	if err != nil && !plain {
		return "", err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.record(ctx, cwd, loc, plain)
}

// record requires the caller to hold r.mu.
func (r *Registry) record(ctx context.Context, cwd string, loc gittree.Location, plain bool) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	q := r.q.WithTx(tx)

	var wsID string
	if plain {
		dir := cleanDir(cwd)
		projID := idFor("plain:" + dir)
		if _, err := q.UpsertProject(ctx, gen.UpsertProjectParams{ID: projID, Name: filepath.Base(dir), Kind: "plain", Dir: dir}); err != nil {
			return "", err
		}
		wsID = idFor(dir)
		if err := q.UpsertWorkspace(ctx, gen.UpsertWorkspaceParams{ID: wsID, ProjectID: projID, Dir: dir, IsMain: true}); err != nil {
			return "", err
		}
	} else {
		if loc.WorktreeRoot == "" {
			return "", nil // bare repo, no working tree to adopt
		}
		projID := idFor(loc.GitDir)
		if _, err := q.UpsertProject(ctx, gen.UpsertProjectParams{ID: projID, Name: projectName(loc), Kind: "git", Dir: loc.GitDir}); err != nil {
			return "", err
		}
		wsID = idFor(loc.WorktreeRoot)
		if err := q.UpsertWorkspace(ctx, gen.UpsertWorkspaceParams{ID: wsID, ProjectID: projID, Dir: loc.WorktreeRoot, IsMain: loc.IsMain}); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return wsID, nil
}

// WorkspaceDir returns the working directory of the workspace with the given
// (node-local) id. ok is false when no such workspace is recorded.
func (r *Registry) WorkspaceDir(ctx context.Context, id string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, err := r.q.GetWorkspace(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return w.Dir, true, nil
}

// WorkspaceInfo reports ok false when no such workspace is recorded.
func (r *Registry) WorkspaceInfo(ctx context.Context, id string) (dir string, isMain bool, projectID string, ok bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, err := r.q.GetWorkspace(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, "", false, nil
	}
	if err != nil {
		return "", false, "", false, err
	}
	return w.Dir, w.IsMain, w.ProjectID, true, nil
}

// ProjectInfo returns a project's display name and main working-tree directory
// (the dir to run git in). mainDir is empty for a bare repo with no main tree.
func (r *Registry) ProjectInfo(ctx context.Context, id string) (name, mainDir string, ok bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.q.GetProject(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	dir, derr := r.q.MainWorkspaceDir(ctx, id)
	if derr != nil && !errors.Is(derr, sql.ErrNoRows) {
		return "", "", false, derr
	}
	return p.Name, dir, true, nil
}

var ErrUnknownProject = errors.New("unknown project")

func (r *Registry) RenameProject(ctx context.Context, id, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.q.RenameProject(ctx, gen.RenameProjectParams{Name: name, ID: id})
	return projectUpdated(id, n, err)
}

func (r *Registry) SetProjectHidden(ctx context.Context, id string, hidden bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.q.SetProjectHidden(ctx, gen.SetProjectHiddenParams{Hidden: hidden, ID: id})
	return projectUpdated(id, n, err)
}

func (r *Registry) SetProjectPinned(ctx context.Context, id string, pinned bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.q.SetProjectPinned(ctx, gen.SetProjectPinnedParams{Pinned: pinned, ID: id})
	return projectUpdated(id, n, err)
}

func projectUpdated(id string, rows int64, err error) error {
	if err == nil && rows == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownProject, id)
	}
	return err
}

// Snapshot reconciles every Project against the live filesystem and returns the
// tree with live branch/head filled in.
func (r *Registry) Snapshot(ctx context.Context) ([]Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out, promoted, err := r.snapshot(ctx)
	if err == nil && promoted {
		out, _, err = r.snapshot(ctx) // list the git projects the promotion added
	}
	return out, err
}

func (r *Registry) snapshot(ctx context.Context) (out []Project, promoted bool, err error) {
	rows, err := r.q.ListProjects(ctx)
	if err != nil {
		return nil, false, err
	}
	out = make([]Project, 0, len(rows))
	for _, p := range rows {
		live, pr, err := r.reconcile(ctx, p)
		if err != nil {
			return nil, false, err
		}
		promoted = promoted || pr
		if p, err = r.q.GetProject(ctx, p.ID); err != nil { // reconcile may have changed is_gone
			return nil, false, err
		}
		wss, err := r.q.ListWorkspacesByProject(ctx, p.ID)
		if err != nil {
			return nil, false, err
		}
		out = append(out, buildProject(p, wss, live))
	}
	return out, promoted, nil
}

// reconcile updates one Project's persisted workspaces from the live filesystem
// and returns the live worktree state keyed by dir (empty for a plain or gone
// project). promoted is true when a plain project turned into a git one.
func (r *Registry) reconcile(ctx context.Context, p gen.Project) (live map[string]gittree.Worktree, promoted bool, err error) {
	live = map[string]gittree.Worktree{}

	if p.Kind == "plain" {
		if !dirExists(p.Dir) {
			return live, false, r.markGone(ctx, p.ID)
		}
		if pathExists(filepath.Join(p.Dir, ".git")) {
			if p.IsGone {
				return live, false, nil // its workspace moved to the git project
			}
			if promoted, err := r.promote(ctx, p); promoted || err != nil {
				return live, promoted, err
			}
		}
		return live, false, r.touchPlain(ctx, p)
	}

	worktrees, err := gittree.ListWorktrees(ctx, p.Dir)
	if err != nil {
		return live, false, r.markGone(ctx, p.ID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return live, false, err
	}
	defer tx.Rollback()
	q := r.q.WithTx(tx)
	if err := q.MarkWorkspacesGone(ctx, p.ID); err != nil {
		return live, false, err
	}
	for _, wt := range worktrees {
		if wt.Bare {
			continue
		}
		live[wt.Dir] = wt
		if err := q.UpsertWorkspace(ctx, gen.UpsertWorkspaceParams{ID: idFor(wt.Dir), ProjectID: p.ID, Dir: wt.Dir, IsMain: wt.IsMain}); err != nil {
			return live, false, err
		}
	}
	if err := q.TouchProject(ctx, p.ID); err != nil {
		return live, false, err
	}
	return live, false, tx.Commit()
}

// promote moves a plain project whose directory became a repository root (git
// init) onto its git project, and marks the plain project gone.
func (r *Registry) promote(ctx context.Context, p gen.Project) (bool, error) {
	loc, err := gittree.Resolve(ctx, p.Dir)
	if err != nil || loc.WorktreeRoot != p.Dir {
		return false, nil
	}
	if _, err := r.record(ctx, p.Dir, loc, false); err != nil {
		return false, err
	}
	return true, r.markGone(ctx, p.ID)
}

func (r *Registry) markGone(ctx context.Context, projID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := r.q.WithTx(tx)
	if err := q.MarkProjectGone(ctx, projID); err != nil {
		return err
	}
	if err := q.MarkWorkspacesGone(ctx, projID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) touchPlain(ctx context.Context, p gen.Project) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := r.q.WithTx(tx)
	if err := q.TouchProject(ctx, p.ID); err != nil {
		return err
	}
	if err := q.UpsertWorkspace(ctx, gen.UpsertWorkspaceParams{ID: idFor(p.Dir), ProjectID: p.ID, Dir: p.Dir, IsMain: true}); err != nil {
		return err
	}
	return tx.Commit()
}

func buildProject(p gen.Project, wss []gen.Workspace, live map[string]gittree.Worktree) Project {
	proj := Project{
		ID: p.ID, Name: p.Name, Kind: p.Kind, Dir: p.Dir,
		IsGone: p.IsGone, Hidden: p.Hidden, Pinned: p.Pinned,
		CreatedAt: p.CreatedAt, LastSeenAt: p.LastSeenAt,
	}
	for _, w := range wss {
		ws := Workspace{
			ID: w.ID, Dir: w.Dir, IsMain: w.IsMain, IsGone: w.IsGone,
			CreatedAt: w.CreatedAt, LastSeenAt: w.LastSeenAt,
		}
		if lw, ok := live[w.Dir]; ok {
			ws.Branch = lw.Branch
			ws.Head = lw.Head
		}
		if w.IsMain {
			proj.Root = w.Dir
		}
		proj.Workspaces = append(proj.Workspaces, ws)
	}
	return proj
}

func idFor(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return hex.EncodeToString(sum[:])[:32]
}

func cleanDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Clean(dir)
}

// projectName is the display name for a git project: the main worktree's
// basename, or the repository name for a bare repo.
func projectName(loc gittree.Location) string {
	return gittree.NameFromGitDir(loc.GitDir)
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
