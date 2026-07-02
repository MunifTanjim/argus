package codex

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestLoadThreadMeta(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads(
		id TEXT PRIMARY KEY,
		rollout_path TEXT NOT NULL, cwd TEXT NOT NULL, model TEXT,
		title TEXT NOT NULL, tokens_used INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO threads VALUES
		('a','/r/a.jsonl','/cwd/a','gpt-5.5','title a',42),
		('b','/r/b.jsonl','/cwd/b',NULL,'title b',0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	got, err := loadThreadMeta([]string{"a", "b", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows; want 2 (missing id skipped)", len(got))
	}
	a := got["a"]
	if a.rolloutPath != "/r/a.jsonl" || a.cwd != "/cwd/a" || a.model != "gpt-5.5" || a.title != "title a" || a.tokens != 42 {
		t.Errorf("row a wrong: %+v", a)
	}
	if b, ok := got["b"]; !ok || b.model != "" || b.title != "title b" {
		t.Errorf("NULL model should COALESCE to empty without aborting: ok=%v %+v", ok, b)
	}
}

func TestLoadThreadMetaMissingDB(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir()) // no state_5.sqlite
	got, err := loadThreadMeta([]string{"a"})
	if err != nil {
		t.Fatalf("missing DB should degrade, not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestLoadThreadMetaNoIDs(t *testing.T) {
	got, err := loadThreadMeta(nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("nil ids: got %v err %v", got, err)
	}
}

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
