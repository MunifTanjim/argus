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

// Snapshot reconciles every Project against the live filesystem and returns the
// tree with live branch/head filled in.
func (r *Registry) Snapshot(ctx context.Context) ([]Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rows, err := r.q.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(rows))
	for _, p := range rows {
		live, err := r.reconcile(ctx, p)
		if err != nil {
			return nil, err
		}
		wss, err := r.q.ListWorkspacesByProject(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, buildProject(p, wss, live))
	}
	return out, nil
}

// reconcile updates one Project's persisted workspaces from the live filesystem
// and returns the live worktree state keyed by dir (empty for a plain or gone
// project).
func (r *Registry) reconcile(ctx context.Context, p gen.Project) (map[string]gittree.Worktree, error) {
	live := map[string]gittree.Worktree{}

	if p.Kind == "plain" {
		if !dirExists(p.Dir) {
			return live, r.markGone(ctx, p.ID)
		}
		return live, r.touchPlain(ctx, p)
	}

	worktrees, err := gittree.ListWorktrees(ctx, p.Dir)
	if err != nil {
		return live, r.markGone(ctx, p.ID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return live, err
	}
	defer tx.Rollback()
	q := r.q.WithTx(tx)
	if err := q.MarkWorkspacesGone(ctx, p.ID); err != nil {
		return live, err
	}
	for _, wt := range worktrees {
		if wt.Bare {
			continue
		}
		live[wt.Dir] = wt
		if err := q.UpsertWorkspace(ctx, gen.UpsertWorkspaceParams{ID: idFor(wt.Dir), ProjectID: p.ID, Dir: wt.Dir, IsMain: wt.IsMain}); err != nil {
			return live, err
		}
	}
	if err := q.TouchProject(ctx, p.ID); err != nil {
		return live, err
	}
	return live, tx.Commit()
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
		IsGone: p.IsGone, CreatedAt: p.CreatedAt, LastSeenAt: p.LastSeenAt,
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

func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
