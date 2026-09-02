package atomicfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.dat")

	data := []byte("hello atomicfile")

	// Write creates the file (and parent dir) with correct content.
	if err := atomicfile.Write(path, data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("content mismatch: got %q, want %q", got, data)
	}

	// Check 0600 permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions: got %04o, want 0600", perm)
	}

	// Overwrite replaces content.
	data2 := []byte("updated content")
	if err := atomicfile.Write(path, data2); err != nil {
		t.Fatalf("Write (overwrite): %v", err)
	}
	got2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after overwrite: %v", err)
	}
	if string(got2) != string(data2) {
		t.Fatalf("content mismatch after overwrite: got %q, want %q", got2, data2)
	}

	// A third Write still works.
	data3 := []byte("third write")
	if err := atomicfile.Write(path, data3); err != nil {
		t.Fatalf("Write (third): %v", err)
	}
	got3, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after third write: %v", err)
	}
	if string(got3) != string(data3) {
		t.Fatalf("content mismatch after third write: got %q, want %q", got3, data3)
	}
}
