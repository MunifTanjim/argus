package codex

import (
	"database/sql"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func stateDBPath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state_5.sqlite"), nil
}

// query_only lets SQLite read the live WAL without a writable -shm.
func openCodexDB(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(2000)&_pragma=query_only(true)")
}

// spawnStatuses maps parentID's spawned threads to their status (open or closed).
func spawnStatuses(db *sql.DB, parentID string) map[string]string {
	out := map[string]string{}
	if db == nil || parentID == "" {
		return out
	}
	rows, err := db.Query(`SELECT child_thread_id, status FROM thread_spawn_edges WHERE parent_thread_id = ?`, parentID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var child, status string
		if rows.Scan(&child, &status) == nil {
			out[child] = status
		}
	}
	return out
}

// dbChildByPath finds parentID's spawned thread at agentPath (multi-agent v2).
func dbChildByPath(db *sql.DB, parentID, agentPath string) (childThread, bool) {
	if db == nil || parentID == "" {
		return childThread{}, false
	}
	var c childThread
	err := db.QueryRow(`SELECT t.id, COALESCE(t.agent_nickname, ''), COALESCE(t.rollout_path, '')
		FROM thread_spawn_edges e JOIN threads t ON t.id = e.child_thread_id
		WHERE e.parent_thread_id = ? AND t.agent_path = ?`, parentID, agentPath).Scan(&c.id, &c.nickname, &c.rollout)
	return c, err == nil
}

// dbChildByID reads a thread's nickname and rollout path.
func dbChildByID(db *sql.DB, id string) (childThread, bool) {
	if db == nil {
		return childThread{}, false
	}
	c := childThread{id: id}
	err := db.QueryRow(`SELECT COALESCE(agent_nickname, ''), COALESCE(rollout_path, '') FROM threads WHERE id = ?`, id).Scan(&c.nickname, &c.rollout)
	return c, err == nil
}
