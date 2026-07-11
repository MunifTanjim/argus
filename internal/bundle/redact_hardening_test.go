package bundle

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestRedactLongestFirst: an overlapping shorter literal must not clobber a
// longer one (leaving a "[REDACTED]123" fragment and a misleading zero-match).
func TestRedactLongestFirst(t *testing.T) {
	in := []byte("token sk-abc123 and sk-abc alone")
	out, counts := redactTextBytes(in, []string{"sk-abc", "sk-abc123"})
	if bytes.Contains(out, []byte("123")) {
		t.Fatalf("shorter literal clobbered the longer one: %s", out)
	}
	if want := "token [REDACTED] and [REDACTED] alone"; string(out) != want {
		t.Fatalf("want %q, got %q", want, out)
	}
	if counts["sk-abc123"] != 1 {
		t.Fatalf("want longer literal counted once, got %d", counts["sk-abc123"])
	}
	if counts["sk-abc"] != 1 {
		t.Fatalf("want shorter literal counted once (standalone), got %d", counts["sk-abc"])
	}
}

// TestRedactSQLiteReclaimsFreedPage: a secret in a row deleted before export
// lingers in a freed page with no live cell to edit. The unconditional VACUUM
// must still scrub it from the raw file.
func TestRedactSQLiteReclaimsFreedPage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conv.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t(v TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t(v) VALUES(?)`, "sk-secret leaked"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM t`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("sk-secret")) {
		t.Skip("sqlite reclaimed the page on its own; nothing to prove here")
	}

	// No live cell holds the secret, so there are zero edits — the fix is that
	// VACUUM runs anyway.
	counts, _, err := redactSQLite(path, []string{"sk-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if counts["sk-secret"] != 0 {
		t.Fatalf("expected no live-cell edits, got %d", counts["sk-secret"])
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sk-secret")) {
		t.Fatal("freed-page secret survived redaction; VACUUM must reclaim it")
	}
}

// TestRedactSQLiteWALLeavesNoSidecar: a WAL-mode source db must redact and be
// converted to DELETE mode, leaving no -wal/-shm sidecar for WriteDir to zip.
func TestRedactSQLiteWALLeavesNoSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conv.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t(v TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t(v) VALUES(?)`, "sk-secret"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, _, err := redactSQLite(path, []string{"sk-secret"}); err != nil {
		t.Fatal(err)
	}
	for _, sfx := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + sfx); !os.IsNotExist(err) {
			t.Fatalf("sidecar %s%s must not survive redaction (err=%v)", filepath.Base(path), sfx, err)
		}
	}
	after, _, err := scanSQLite(path, []string{"sk-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if after["sk-secret"] != 0 {
		t.Fatalf("secret survives in WAL-origin db: %d", after["sk-secret"])
	}
}

// TestVerifyResiduals: the backstop reports surviving literals, and honors the
// skip set so a leak already flagged by a specific warning isn't doubled.
func TestVerifyResiduals(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leak.txt"), []byte("has sk-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clean.txt"), []byte("nothing here"), 0o644); err != nil {
		t.Fatal(err)
	}

	warns, err := verifyResiduals(dir, []string{"sk-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "leak.txt") {
		t.Fatalf("want one residual naming leak.txt, got %v", warns)
	}

	warns, err = verifyResiduals(dir, []string{"sk-secret"}, map[string]bool{"leak.txt": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("want no residuals when the file is skipped, got %v", warns)
	}
}

// TestRedactTreeSkippedTableWarnedOnce: a secret in a WITHOUT ROWID table can't
// be scrubbed and genuinely remains in the copied db, but must be reported by
// exactly one warning (the skip warning) — the residual backstop must not
// double-report it.
func TestRedactTreeSkippedTableWarnedOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifestName),
		mustJSON(t, Manifest{FormatVersion: 1, Agent: "claude", Entry: "conv.db", Metadata: Metadata{}}), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "conv.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE s(k TEXT PRIMARY KEY, v TEXT) WITHOUT ROWID`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO s VALUES(?, ?)`, "k1", "sk-secret"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	dst := filepath.Join(t.TempDir(), "out")
	rep, err := RedactTree(dir, dst, []string{"sk-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, w := range rep.Warnings {
		if strings.Contains(w, "conv.db") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one warning for conv.db, got %d: %v", n, rep.Warnings)
	}
	// The secret really is still there (we can't rewrite a rowid-less table).
	raw, err := os.ReadFile(filepath.Join(dst, "conv.db"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("sk-secret")) {
		t.Fatal("test premise broken: expected the un-redactable secret to remain")
	}
}
