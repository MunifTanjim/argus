-- name: UpsertProject :one
INSERT INTO project (id, name, kind, dir)
VALUES (?, ?, ?, ?)
ON CONFLICT (dir) DO UPDATE SET
    is_gone = 0,
    last_seen_at = CURRENT_TIMESTAMP
RETURNING id;

-- name: TouchProject :exec
UPDATE project SET is_gone = 0, last_seen_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: MarkProjectGone :exec
UPDATE project SET is_gone = 1 WHERE id = ?;

-- name: RenameProject :execrows
UPDATE project SET name = ? WHERE id = ?;

-- name: SetProjectHidden :execrows
UPDATE project SET hidden = ? WHERE id = ?;

-- name: SetProjectPinned :execrows
UPDATE project SET pinned = ? WHERE id = ?;

-- name: UpsertWorkspace :exec
INSERT INTO workspace (id, project_id, dir, is_main)
VALUES (?, ?, ?, ?)
ON CONFLICT (dir) DO UPDATE SET
    project_id = excluded.project_id,
    is_main = excluded.is_main,
    is_gone = 0,
    last_seen_at = CURRENT_TIMESTAMP;

-- name: DeleteProject :execrows
DELETE FROM project WHERE id = ?;

-- name: MarkWorkspacesGone :exec
UPDATE workspace SET is_gone = 1 WHERE project_id = ?;

-- name: ListProjects :many
SELECT id, name, kind, dir, is_gone, created_at, last_seen_at, hidden, pinned
FROM project
ORDER BY pinned DESC, name;

-- name: ListWorkspacesByProject :many
SELECT id, project_id, dir, is_main, is_gone, created_at, last_seen_at, target_branch
FROM workspace
WHERE project_id = ?
ORDER BY is_main DESC, dir;

-- name: GetWorkspace :one
SELECT id, project_id, dir, is_main, is_gone, created_at, last_seen_at, target_branch
FROM workspace
WHERE id = ?;

-- name: SetWorkspaceTarget :exec
UPDATE workspace SET target_branch = ? WHERE id = ?;

-- name: MainWorkspaceDir :one
SELECT dir
FROM workspace
WHERE project_id = ? AND is_main = 1
LIMIT 1;

-- name: GetProject :one
SELECT id, name, kind, dir, is_gone, created_at, last_seen_at, hidden, pinned
FROM project
WHERE id = ?;
