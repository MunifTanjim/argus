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
	ID   string
	Name string
	Kind string
	Dir  string
	Root string // open path: the main workspace's dir ("" for a bare repo)
	// DefaultBranch is resolved live; "" for a plain or unresolvable project.
	DefaultBranch string
	IsGone        bool
	Error         string // git failed on it this list; its rows are the last known
	Hidden        bool
	Pinned        bool
	CreatedAt     time.Time
	LastSeenAt    time.Time
	Workspaces    []Workspace
}

// Workspace is one git worktree, or the single directory of a plain project.
// Branch and Head are empty for a gone or plain workspace; Branch is empty for
// a detached HEAD.
type Workspace struct {
	ID     string
	Dir    string
	IsMain bool
	IsGone bool
	Branch string
	Head   string
	// TargetBranch is the stored target, or the project's default branch when
	// unset.
	TargetBranch string
	CreatedAt    time.Time
	LastSeenAt   time.Time
}

type Registry struct {
	db *sql.DB
	q  *gen.Queries
	mu sync.Mutex
	// records counts committed records; recordedAt holds a project's count at
	// its last record, so apply can tell which projects changed after a probe.
	records    uint64
	recordedAt map[string]uint64
}

func New(sqlDB *sql.DB) *Registry {
	return &Registry{db: sqlDB, q: gen.New(sqlDB), recordedAt: map[string]uint64{}}
}

// AdoptSession records the Project and Workspace that contain cwd and returns
// the workspace id. A non-git cwd becomes a plain Project with one workspace.
// An empty cwd, or a cwd inside a bare repo with no working tree, is a no-op.
func (r *Registry) AdoptSession(ctx context.Context, cwd string) (string, error) {
	wsID, _, err := r.adopt(ctx, cwd, nil)
	return wsID, err
}

// AdoptWorkspace records a newly created worktree and its target branch in one
// transaction, so a failure leaves no half-registered workspace.
func (r *Registry) AdoptWorkspace(ctx context.Context, dir, target string) (string, error) {
	wsID, _, err := r.adopt(ctx, dir, &target)
	return wsID, err
}

// Adopt is AdoptSession that also reports whether the workspace is new.
func (r *Registry) Adopt(ctx context.Context, cwd string) (string, bool, error) {
	return r.adopt(ctx, cwd, nil)
}

func (r *Registry) adopt(ctx context.Context, cwd string, target *string) (string, bool, error) {
	if cwd == "" {
		return "", false, nil
	}
	loc, err := gittree.Resolve(ctx, cwd)
	plain := errors.Is(err, gittree.ErrNotRepo)
	if err != nil && !plain {
		return "", false, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.record(ctx, cwd, loc, plain, target)
}

// record requires the caller to hold r.mu.
func (r *Registry) record(ctx context.Context, cwd string, loc gittree.Location, plain bool, target *string) (string, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	q := r.q.WithTx(tx)

	var projID, wsID, wsDir string
	var wsIsMain bool
	if plain {
		dir := cleanDir(cwd)
		projID = idFor("plain:" + dir)
		wsID = idFor(dir)
		wsDir = dir
		wsIsMain = true
		if _, err := q.UpsertProject(ctx, gen.UpsertProjectParams{ID: projID, Name: filepath.Base(dir), Kind: "plain", Dir: dir}); err != nil {
			return "", false, err
		}
	} else {
		if loc.WorktreeRoot == "" {
			return "", false, nil // bare repo, no working tree to adopt
		}
		projID = idFor(loc.GitDir)
		wsID = idFor(loc.WorktreeRoot)
		wsDir = loc.WorktreeRoot
		wsIsMain = loc.IsMain
		if _, err := q.UpsertProject(ctx, gen.UpsertProjectParams{ID: projID, Name: projectName(loc), Kind: "git", Dir: loc.GitDir}); err != nil {
			return "", false, err
		}
	}

	_, gerr := q.GetWorkspace(ctx, wsID)
	isNew := errors.Is(gerr, sql.ErrNoRows)

	if err := q.UpsertWorkspace(ctx, gen.UpsertWorkspaceParams{ID: wsID, ProjectID: projID, Dir: wsDir, IsMain: wsIsMain}); err != nil {
		return "", false, err
	}
	if target != nil {
		if err := q.SetWorkspaceTarget(ctx, gen.SetWorkspaceTargetParams{TargetBranch: *target, ID: wsID}); err != nil {
			return "", false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	r.records++
	r.recordedAt[projID] = r.records
	return wsID, isNew, nil
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

// ProjectKind returns a project's kind: "git" or "plain".
func (r *Registry) ProjectKind(ctx context.Context, id string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.q.GetProject(ctx, id)
	if err != nil {
		return "", err
	}
	return p.Kind, nil
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

// ForgetProject drops a project and, by cascade, its workspaces from the
// registry. The files stay; a session that starts there later adopts it again.
func (r *Registry) ForgetProject(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.q.DeleteProject(ctx, id)
	return projectUpdated(id, n, err)
}

func (r *Registry) ForgetWorkspace(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.q.DeleteWorkspace(ctx, id)
}

func projectUpdated(id string, rows int64, err error) error {
	if err == nil && rows == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownProject, id)
	}
	return err
}

func (r *Registry) SetWorkspaceTarget(ctx context.Context, id, branch string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.q.SetWorkspaceTarget(ctx, gen.SetWorkspaceTargetParams{TargetBranch: branch, ID: id})
}

// TargetBranch returns a workspace's stored target, or its project's default
// branch when unset. ok is false when no such workspace is recorded.
func (r *Registry) TargetBranch(ctx context.Context, id string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, err := r.q.GetWorkspace(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if w.TargetBranch != "" {
		return w.TargetBranch, true, nil
	}
	return gittree.DefaultBranch(ctx, w.Dir), true, nil
}

// Snapshot reconciles every Project against the live filesystem and returns the
// tree with live branch/head filled in.
func (r *Registry) Snapshot(ctx context.Context) ([]Project, error) {
	out, promoted, err := r.snapshot(ctx)
	if err == nil && promoted {
		out, _, err = r.snapshot(ctx) // list the git projects the promotion added
	}
	return out, err
}

// snapshot probes the filesystem and git without the lock, so an adopt does not
// wait on git, then applies the results under it.
func (r *Registry) snapshot(ctx context.Context) ([]Project, bool, error) {
	r.mu.Lock()
	rows, err := r.q.ListProjects(ctx)
	since := r.records
	r.mu.Unlock()
	if err != nil {
		return nil, false, err
	}
	return r.apply(ctx, rows, probeAll(ctx, rows), since)
}

// since is the record count when the probes began.
func (r *Registry) apply(ctx context.Context, rows []gen.Project, probes []probe, since uint64) (out []Project, promoted bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out = make([]Project, 0, len(rows))
	for i, row := range rows {
		p, err := r.q.GetProject(ctx, row.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue // forgotten since the list
		}
		if err != nil {
			return nil, false, err
		}
		rec, err := r.reconcile(ctx, p, probes[i], r.recordedAt[p.ID] > since)
		if err != nil {
			return nil, false, err
		}
		promoted = promoted || rec.promoted
		if p, err = r.q.GetProject(ctx, p.ID); err != nil { // reconcile may have changed is_gone
			return nil, false, err
		}
		wss, err := r.q.ListWorkspacesByProject(ctx, p.ID)
		if err != nil {
			return nil, false, err
		}
		def := ""
		if p.Kind == "git" && !p.IsGone && rec.gitErr == "" {
			def = probes[i].defaultBranch
		}
		proj := buildProject(p, wss, rec.live, def)
		proj.Error = rec.gitErr
		out = append(out, proj)
	}
	return out, promoted, nil
}

// probe is one project's live filesystem and git state, gathered without the
// lock or the database.
type probe struct {
	exists        bool               // the project's dir (the git dir, for git) exists
	gitMarker     bool               // plain: the dir now holds a .git
	worktrees     []gittree.Worktree // git: unset when gitErr is
	gitErr        error
	defaultBranch string
}

const probeWorkers = 8

func probeAll(ctx context.Context, rows []gen.Project) []probe {
	out := make([]probe, len(rows))
	sem := make(chan struct{}, probeWorkers)
	var wg sync.WaitGroup
	for i, p := range rows {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = probeProject(ctx, p)
		})
	}
	wg.Wait()
	return out
}

func probeProject(ctx context.Context, p gen.Project) probe {
	if p.Kind == "plain" {
		return probe{exists: dirExists(p.Dir), gitMarker: pathExists(filepath.Join(p.Dir, ".git"))}
	}
	wts, err := gittree.ListWorktrees(ctx, p.Dir)
	if err != nil {
		return probe{exists: pathExists(p.Dir), gitErr: err}
	}
	return probe{exists: true, worktrees: wts, defaultBranch: gittree.DefaultBranch(ctx, p.Dir)}
}

type reconciled struct {
	live     map[string]gittree.Worktree // keyed by dir; empty for a plain or gone project
	promoted bool                        // a plain project turned into a git one
	gitErr   string                      // git failed on a project whose git dir exists
}

// reconcile updates one Project's persisted workspaces from its probe. A git
// failure on a project whose git dir still exists changes nothing and is
// reported, so the next list that git answers recovers it.
func (r *Registry) reconcile(ctx context.Context, p gen.Project, pr probe, recordedSinceProbe bool) (reconciled, error) {
	res := reconciled{live: map[string]gittree.Worktree{}}

	if p.Kind == "plain" {
		if !pr.exists {
			return res, r.markGone(ctx, p.ID)
		}
		if pr.gitMarker {
			if p.IsGone {
				return res, nil // its workspace moved to the git project
			}
			promoted, err := r.promote(ctx, p)
			if promoted || err != nil {
				res.promoted = promoted
				return res, err
			}
		}
		return res, r.syncPlain(ctx, p)
	}

	if pr.gitErr != nil {
		if !pr.exists {
			return res, r.markGone(ctx, p.ID)
		}
		res.gitErr = pr.gitErr.Error()
		return res, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	q := r.q.WithTx(tx)
	// A worktree recorded after the probe began is missing from the probe.
	if !recordedSinceProbe {
		if err := q.MarkWorkspacesGone(ctx, p.ID); err != nil {
			return res, err
		}
	}
	for _, wt := range pr.worktrees {
		if wt.Bare {
			continue
		}
		res.live[wt.Dir] = wt
		if err := q.SyncWorkspace(ctx, gen.SyncWorkspaceParams{ID: idFor(wt.Dir), ProjectID: p.ID, Dir: wt.Dir, IsMain: wt.IsMain}); err != nil {
			return res, err
		}
	}
	if err := q.ReviveProject(ctx, p.ID); err != nil {
		return res, err
	}
	return res, tx.Commit()
}

// promote moves a plain project whose directory became a repository root (git
// init) onto its git project, and marks the plain project gone.
func (r *Registry) promote(ctx context.Context, p gen.Project) (bool, error) {
	loc, err := gittree.Resolve(ctx, p.Dir)
	if err != nil || loc.WorktreeRoot != p.Dir {
		return false, nil
	}
	if _, _, err := r.record(ctx, p.Dir, loc, false, nil); err != nil {
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

func (r *Registry) syncPlain(ctx context.Context, p gen.Project) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := r.q.WithTx(tx)
	if err := q.ReviveProject(ctx, p.ID); err != nil {
		return err
	}
	if err := q.SyncWorkspace(ctx, gen.SyncWorkspaceParams{ID: idFor(p.Dir), ProjectID: p.ID, Dir: p.Dir, IsMain: true}); err != nil {
		return err
	}
	return tx.Commit()
}

func buildProject(p gen.Project, wss []gen.Workspace, live map[string]gittree.Worktree, def string) Project {
	proj := Project{
		ID: p.ID, Name: p.Name, Kind: p.Kind, Dir: p.Dir, DefaultBranch: def,
		IsGone: p.IsGone, Hidden: p.Hidden, Pinned: p.Pinned,
		CreatedAt: p.CreatedAt, LastSeenAt: p.LastSeenAt,
	}
	for _, w := range wss {
		ws := Workspace{
			ID: w.ID, Dir: w.Dir, IsMain: w.IsMain, IsGone: w.IsGone,
			CreatedAt: w.CreatedAt, LastSeenAt: w.LastSeenAt,
		}
		ws.TargetBranch = w.TargetBranch
		if ws.TargetBranch == "" {
			ws.TargetBranch = def
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
