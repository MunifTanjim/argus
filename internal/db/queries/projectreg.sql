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

-- name: UpsertWorkspace :exec
INSERT INTO workspace (id, project_id, dir, is_main)
VALUES (?, ?, ?, ?)
ON CONFLICT (dir) DO UPDATE SET
    is_main = excluded.is_main,
    is_gone = 0,
    last_seen_at = CURRENT_TIMESTAMP;

-- name: MarkWorkspacesGone :exec
UPDATE workspace SET is_gone = 1 WHERE project_id = ?;

-- name: ListProjects :many
SELECT id, name, kind, dir, is_gone, created_at, last_seen_at
FROM project
ORDER BY name;

-- name: ListWorkspacesByProject :many
SELECT id, project_id, dir, is_main, is_gone, created_at, last_seen_at
FROM workspace
WHERE project_id = ?
ORDER BY is_main DESC, dir;
