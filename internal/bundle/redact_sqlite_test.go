package bundle

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "conv.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE meta(note TEXT, blob BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meta(note, blob) VALUES(?, ?)`,
		"token sk-secret here", []byte("blob has sk-secret too")); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScanSQLite(t *testing.T) {
	path := newTestDB(t)
	counts, _, err := scanSQLite(path, []string{"sk-secret", "absent"})
	if err != nil {
		t.Fatal(err)
	}
	if counts["sk-secret"] != 2 {
		t.Fatalf("want 2, got %d", counts["sk-secret"])
	}
	if counts["absent"] != 0 {
		t.Fatalf("want 0, got %d", counts["absent"])
	}
}

func TestRedactSQLite(t *testing.T) {
	path := newTestDB(t)
	counts, _, err := redactSQLite(path, []string{"sk-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if counts["sk-secret"] != 2 {
		t.Fatalf("want 2 replaced, got %d", counts["sk-secret"])
	}
	// Cells no longer contain the secret.
	after, _, err := scanSQLite(path, []string{"sk-secret", RedactPlaceholder})
	if err != nil {
		t.Fatal(err)
	}
	if after["sk-secret"] != 0 {
		t.Fatalf("secret still present in cells: %d", after["sk-secret"])
	}
	if after[RedactPlaceholder] != 2 {
		t.Fatalf("want 2 placeholders, got %d", after[RedactPlaceholder])
	}
	// Post-VACUUM the raw file bytes must not contain the secret in freed pages.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sk-secret")) {
		t.Fatal("secret survives in raw db bytes after vacuum")
	}
}

func TestScanWarnsSkippedSQLiteTable(t *testing.T) {
	dir := t.TempDir()
	manifest := Manifest{FormatVersion: 1, Agent: "claude", Entry: "conv.db", Metadata: Metadata{}}
	mb, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestName), mb, 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "conv.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE secrets(k TEXT PRIMARY KEY, v TEXT) WITHOUT ROWID`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO secrets VALUES(?, ?)`, "key1", "sk-secret"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	rep, err := Scan(dir, []string{"sk-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range rep.Warnings {
		if strings.Contains(w, `"secrets"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("want warning naming table \"secrets\", got %v", rep.Warnings)
	}
}
