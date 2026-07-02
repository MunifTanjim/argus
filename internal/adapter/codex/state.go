package codex

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// From Codex's state DB.
type threadMeta struct {
	rolloutPath string
	cwd         string
	model       string
	title       string
	tokens      int
}

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

// A missing DB yields an empty map and no error.
func loadThreadMeta(ids []string) (map[string]threadMeta, error) {
	out := map[string]threadMeta{}
	if len(ids) == 0 {
		return out, nil
	}
	path, err := stateDBPath()
	if err != nil {
		return out, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return out, nil // schema/version drift: degrade, don't fail
		}
		return out, err
	}

	db, err := openCodexDB(path)
	if err != nil {
		return out, err
	}
	defer db.Close()

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	// model is nullable; COALESCE prevents a nil scan failure.
	q := fmt.Sprintf(`SELECT id, rollout_path, cwd, COALESCE(model,''), title, tokens_used
		FROM threads WHERE id IN (%s)`, placeholders)
	rows, err := db.Query(q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var m threadMeta
		if err := rows.Scan(&id, &m.rolloutPath, &m.cwd, &m.model, &m.title, &m.tokens); err != nil {
			return out, err
		}
		out[id] = m
	}
	return out, rows.Err()
}

func loadSpawnEdges(path string) map[string]string {
	out := map[string]string{}
	if _, err := os.Stat(path); err != nil {
		return out
	}
	db, err := openCodexDB(path)
	if err != nil {
		return out
	}
	defer db.Close()

	rows, err := db.Query(`SELECT child_thread_id, status FROM thread_spawn_edges`)
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
