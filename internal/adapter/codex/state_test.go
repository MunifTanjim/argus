package codex

import (
	"database/sql"
	"testing"
)

func TestLoadSpawnEdges(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/state.sqlite"
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE thread_spawn_edges (
		parent_thread_id TEXT NOT NULL,
		child_thread_id TEXT NOT NULL PRIMARY KEY,
		status TEXT NOT NULL);
		INSERT INTO thread_spawn_edges VALUES ('parentA','childB','closed');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	edges := loadSpawnEdges(p)
	if edges["childB"] != "closed" {
		t.Fatalf("status = %q, want closed", edges["childB"])
	}
}

func TestLoadSpawnEdgesMissingDB(t *testing.T) {
	edges := loadSpawnEdges(t.TempDir() + "/nope.sqlite")
	if len(edges) != 0 {
		t.Fatalf("want empty map, got %d", len(edges))
	}
}
