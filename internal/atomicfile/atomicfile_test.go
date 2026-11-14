package atomicfile_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.dat")

	data := []byte("hello atomicfile")

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

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions: got %04o, want 0600", perm)
	}

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

func TestReplaceKeepsSymlinkAndMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "config")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Replace(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link is no longer a symlink: %v, %v", fi, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %04o, want 0644", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestReplaceNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := atomicfile.Replace(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v; want 0600", fi, err)
	}
}

func TestReplaceNeverCreatesDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	if err := atomicfile.Replace(filepath.Join(dir, "config"), []byte("x")); err == nil {
		t.Fatal("Replace succeeded in a missing directory")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory created; stat err = %v", err)
	}
}

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.dat")
	if err := atomicfile.Create(path, []byte("first")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := atomicfile.Create(path, []byte("second")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second Create: err = %v, want fs.ErrExist", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "first" {
		t.Fatalf("content = %q, err = %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("dir entries = %v, err = %v", entries, err)
	}
}
