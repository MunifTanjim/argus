package codex

import (
	"database/sql"
	"testing"
)

func TestSpawnStatusesScopedToParent(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/state.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE thread_spawn_edges (
		parent_thread_id TEXT NOT NULL,
		child_thread_id TEXT NOT NULL PRIMARY KEY,
		status TEXT NOT NULL);
		INSERT INTO thread_spawn_edges VALUES ('parentA','childB','closed'), ('parentX','childY','open');`)
	if err != nil {
		t.Fatal(err)
	}
	got := spawnStatuses(db, "parentA")
	if got["childB"] != "closed" || len(got) != 1 {
		t.Fatalf("statuses = %v, want only childB closed", got)
	}
}

func TestSpawnStatusesWithoutDB(t *testing.T) {
	if got := spawnStatuses(nil, "parentA"); len(got) != 0 {
		t.Fatalf("want empty map, got %v", got)
	}
}
