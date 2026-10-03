package codex

import (
	"database/sql"
	"os"
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
