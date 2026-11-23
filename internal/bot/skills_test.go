package bot

import (
	"os"
	"path/filepath"
	"testing"
)

func mkSkill(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsPlacedForClaude(t *testing.T) {
	bot, shared := t.TempDir(), t.TempDir()
	mkSkill(t, shared, "web")
	mkSkill(t, shared, "unused")
	mkSkill(t, filepath.Join(bot, "skills"), "mine")
	def := &Definition{Skills: []string{"web", "ghost"}}

	missing, err := PlaceSkills(bot, "claude", shared, def)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != "ghost" {
		t.Fatalf("missing = %v", missing)
	}
	dir := filepath.Join(bot, ".claude", "skills")
	for n, want := range map[string]string{"web": filepath.Join(shared, "web"), "mine": filepath.Join(bot, "skills", "mine")} {
		got, err := os.Readlink(filepath.Join(dir, n))
		if err != nil || got != want {
			t.Fatalf("%s -> %q, %v", n, got, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "unused")); err == nil {
		t.Fatal("unused skill placed")
	}

	// A user's own entry and a foreign symlink survive; a stale one goes.
	if err := os.Mkdir(filepath.Join(dir, "handmade"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "foreign")); err != nil {
		t.Fatal(err)
	}
	if _, err := PlaceSkills(bot, "claude", shared, &Definition{}); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[string]bool{"web": false, "mine": true, "handmade": true, "foreign": true} {
		if _, err := os.Lstat(filepath.Join(dir, n)); (err == nil) != want {
			t.Fatalf("%s present = %v, want %v", n, err == nil, want)
		}
	}

	// Switching CLI moves the links to .agents/skills.
	if _, err := PlaceSkills(bot, "codex", shared, def); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(bot, ".claude", "skills", "mine")); err == nil {
		t.Fatal("claude link left behind")
	}
	if _, err := os.Readlink(filepath.Join(bot, ".agents", "skills", "web")); err != nil {
		t.Fatal(err)
	}
}
