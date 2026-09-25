package db

import (
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argus.db")

	sqlDB, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for _, table := range []string{"project", "workspace"} {
		var name string
		row := sqlDB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table)
		if err := row.Scan(&name); err != nil {
			t.Fatalf("expected table %q: %v", table, err)
		}
	}

	var idx string
	if err := sqlDB.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='workspace_idx_project_id'").Scan(&idx); err != nil {
		t.Fatalf("expected index workspace_idx_project_id: %v", err)
	}
	sqlDB.Close()

	sqlDB2, err := Open(path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	sqlDB2.Close()
}
