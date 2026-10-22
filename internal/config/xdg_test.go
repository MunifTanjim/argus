package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func readOnlyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if probe, err := os.MkdirTemp(dir, "x"); err == nil {
		_ = os.Remove(probe)
		t.Skip("directory permissions are not enforced")
	}
	return dir
}

func TestPickRuntimeDirUsesExistingDirInReadOnlyBase(t *testing.T) {
	base := t.TempDir()
	want := filepath.Join(base, ProjectName)
	if err := os.Mkdir(want, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o700) })
	got, fallback := pickRuntimeDir(base, t.TempDir())
	if got != want || fallback {
		t.Fatalf("got %q fallback=%v, want %q", got, fallback, want)
	}
}

func TestPickRuntimeDirUsesMissingDirInWritableBase(t *testing.T) {
	base := t.TempDir()
	got, fallback := pickRuntimeDir(base, t.TempDir())
	if want := filepath.Join(base, ProjectName); got != want || fallback {
		t.Fatalf("got %q fallback=%v, want %q", got, fallback, want)
	}
}

func TestPickRuntimeDirFallsBackForMissingDirInReadOnlyBase(t *testing.T) {
	base := readOnlyDir(t)
	tmp := t.TempDir()
	got, fallback := pickRuntimeDir(base, tmp)
	if want := filepath.Join(tmp, fmt.Sprintf("%s-%d", ProjectName, os.Getuid())); got != want || !fallback {
		t.Fatalf("got %q fallback=%v, want %q", got, fallback, want)
	}
}

func TestPickRuntimeDirFallsBackWithoutBase(t *testing.T) {
	if _, fallback := pickRuntimeDir("", t.TempDir()); !fallback {
		t.Fatal("expected fallback")
	}
}
