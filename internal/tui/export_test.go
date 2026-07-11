package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteExportFileDedupes(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	p1, err := writeExportFile("s.argus", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p1) != "s.argus" {
		t.Fatalf("first write name: %s", p1)
	}
	p2, err := writeExportFile("s.argus", []byte("b"))
	if err != nil {
		t.Fatal(err)
	}
	if p2 == p1 {
		t.Fatalf("second write should not overwrite: %s", p2)
	}
	if _, err := os.Stat(p2); err != nil {
		t.Fatalf("second file missing: %v", err)
	}
	if !filepath.IsAbs(p1) {
		t.Fatalf("expected absolute path, got %s", p1)
	}
	if !filepath.IsAbs(p2) {
		t.Fatalf("expected absolute path for p2, got %s", p2)
	}
}

// The node-supplied filename is untrusted: any directory component is stripped so
// the write can only land in cwd, and a degenerate name is rejected.
func TestWriteExportFileRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)
	// Resolve after chdir: on macOS t.TempDir() is under /var but Getwd reports the
	// real /private/var, so compare against the resolved cwd, not dir.
	wantDir, _ := os.Getwd()

	p, err := writeExportFile("../../etc/evil.argus", []byte("a"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if filepath.Dir(p) != wantDir {
		t.Fatalf("write escaped cwd: %s (want dir %s)", p, wantDir)
	}
	if filepath.Base(p) != "evil.argus" {
		t.Fatalf("directory components not stripped: %s", p)
	}

	if _, err := writeExportFile("..", []byte("a")); err == nil {
		t.Fatal("expected rejection of degenerate filename")
	}
}
