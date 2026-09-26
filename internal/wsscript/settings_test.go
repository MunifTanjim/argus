package wsscript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSettings(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, ".argus", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFilesIsEmpty(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil || s != (Scripts{}) {
		t.Errorf("Load of a repo with no settings = %+v, %v", s, err)
	}
}

func TestLoadSharedAndLocal(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, "settings.toml", "[scripts]\nsetup = \"pnpm install\"\nteardown = \"docker compose down\"\n")
	s, err := Load(dir)
	if err != nil || s.Setup != "pnpm install" || s.Teardown != "docker compose down" {
		t.Fatalf("shared only = %+v, %v", s, err)
	}
	writeSettings(t, dir, "settings.local.toml", "[scripts]\nsetup = \"\"\nteardown = \"make down\"\n")
	s, err = Load(dir)
	if err != nil || s.Setup != "" || s.Teardown != "make down" {
		t.Errorf("local replaces shared per key, \"\" turns a key off: %+v, %v", s, err)
	}
}

func TestLoadLocalOnly(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, "settings.local.toml", "[scripts]\nsetup = \"make\"\n")
	if s, err := Load(dir); err != nil || s.Setup != "make" {
		t.Errorf("local only = %+v, %v", s, err)
	}
}

func TestLoadParseError(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, "settings.toml", "[scripts\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "settings.toml") {
		t.Errorf("a broken file should fail and name itself: %v", err)
	}
}

func TestLoadRejectsWrongType(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, "settings.toml", "[scripts]\nsetup = [\"pnpm\", \"i\"]\n")
	if _, err := Load(dir); err == nil {
		t.Error("a non-string setup should be a parse error")
	}
}
