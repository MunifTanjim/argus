package pagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/pagent/policy"
)

func TestDefinitionRoundTripPreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	src := "# my agent\nid: ag_x\nname: ada\nx-custom:\n  keep: me # trailing\nmodel: opus # why\nnotify: all\n"
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := LoadDefinition(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "ada" || d.Model != "opus" {
		t.Fatalf("def = %+v", d)
	}
	d.Description = "helper"
	d.Notify = ""
	d.IdleClose = 10 * time.Minute
	if err := d.Save(dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "agent.yaml"))
	for _, want := range []string{"# my agent", "x-custom:", "keep: me # trailing", "model: opus # why", "description: helper", "idle-close: 10m0s"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "notify") {
		t.Errorf("cleared key kept:\n%s", out)
	}
	back, err := LoadDefinition(dir)
	if err != nil || back.Description != "helper" || back.IdleClose != 10*time.Minute {
		t.Errorf("reload = %+v, %v", back, err)
	}
}

func TestNewID(t *testing.T) {
	if id := NewID(); !regexp.MustCompile(`^ag_[0-9a-z]{26}$`).MatchString(id) || id == NewID() {
		t.Errorf("id = %q", id)
	}
}

func TestResolvePrefersAgentYAML(t *testing.T) {
	var d config.AgentDefaults
	d.Agent, d.Model, d.Timezone, d.Notify, d.Proactivity = "claude", "sonnet", "auto", "needs-you", "default"
	d.Memory.Mode = "auto"
	d.IdleClose = 30 * time.Minute
	d.MaxConcurrentSessions = 2
	def := &Definition{Agent: "codex", Notify: "all", IdleClose: time.Minute, Memory: MemoryDef{Mode: "off"}}
	s := Resolve(def, d)
	if s.Agent != "codex" || s.Notify != "all" || s.IdleClose != time.Minute || s.MemoryMode != "off" {
		t.Errorf("overrides lost: %+v", s)
	}
	if s.Model != "sonnet" || s.Timezone != "auto" || s.Proactivity != "default" || s.MaxConcurrentSessions != 2 {
		t.Errorf("defaults lost: %+v", s)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestScaffoldCreatesLayoutAndCommits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ada")
	repo, err := Scaffold(context.Background(), dir, &Definition{Name: "ada"}, "nested", false)
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil || repo.Outer {
		t.Fatalf("repo = %+v", repo)
	}
	for _, p := range []string{"agent.yaml", "memory/MEMORY.md", "memory/USER.md", "memory/topics", "memory/log", "skills", "routines", "files"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if n := git(t, dir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Errorf("commits = %s", n)
	}
	msg := git(t, dir, "log", "-1", "--format=%B")
	def, err := LoadDefinition(dir)
	if err != nil || !strings.HasPrefix(def.ID, "ag_") {
		t.Fatalf("def = %+v, %v", def, err)
	}
	for _, want := range []string{"agent: create ada", "Argus-Agent: ada", "Argus-Agent-Id: " + def.ID, "Argus-Source: you"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	ign, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, want := range []string{"CLAUDE.md", "AGENTS.md", "files/"} {
		if !strings.Contains(string(ign), want) {
			t.Errorf(".gitignore lacks %q:\n%s", want, ign)
		}
	}
	if git(t, dir, "status", "--porcelain") != "" {
		t.Errorf("tree not clean: %s", git(t, dir, "status", "--porcelain"))
	}
}

func TestScaffoldCommitFilesKeepsFilesTracked(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ada")
	if _, err := Scaffold(context.Background(), dir, &Definition{Name: "ada"}, "nested", true); err != nil {
		t.Fatal(err)
	}
	ign, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Contains(string(ign), "files/") {
		t.Errorf(".gitignore = %s", ign)
	}
}

func TestScaffoldIdempotent(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "ada")
	if _, err := Scaffold(ctx, dir, &Definition{Name: "ada"}, "nested", false); err != nil {
		t.Fatal(err)
	}
	first, _ := LoadDefinition(dir)
	if err := os.Remove(filepath.Join(dir, "memory", "USER.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold(ctx, dir, &Definition{Name: "other"}, "nested", false); err != nil {
		t.Fatal(err)
	}
	second, _ := LoadDefinition(dir)
	if second.ID != first.ID || second.Name != "ada" {
		t.Errorf("definition changed: %+v -> %+v", first, second)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory", "USER.md")); err != nil {
		t.Errorf("missing piece not restored")
	}
	if n := git(t, dir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Errorf("commits = %s", n)
	}
}

func TestImportInstructionFileSkipsGenerated(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := ImportInstructionFile(dir); ok || err != nil {
		t.Fatalf("empty dir: %v %v", ok, err)
	}
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(GeneratedMarker+"\nstuff\n"), 0o644)
	if _, ok, _ := ImportInstructionFile(dir); ok {
		t.Error("generated file offered")
	}
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("be kind\n"), 0o644)
	got, ok, err := ImportInstructionFile(dir)
	if err != nil || !ok || got != "be kind\n" {
		t.Errorf("got %q %v %v", got, ok, err)
	}
}

func TestDefinitionApprovalsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := "id: a1\nname: x\napprovals:\n  - {tool: Bash, target: git, decision: allow}\n"
	if err := os.WriteFile(filepath.Join(dir, DefinitionFile), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := LoadDefinition(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Approvals) != 1 || d.Approvals[0].Target != "git" || d.Approvals[0].Decision != "allow" {
		t.Fatalf("approvals: %+v", d.Approvals)
	}
	d.Approvals = append(d.Approvals, policy.Rule{Tool: "Edit", Decision: "ask"})
	if err := d.Save(dir); err != nil {
		t.Fatal(err)
	}
	d2, err := LoadDefinition(dir)
	if err != nil || len(d2.Approvals) != 2 || d2.Approvals[1].Tool != "Edit" {
		t.Fatalf("reload: %v %+v", err, d2)
	}
}

func TestResolveNotificationSettings(t *testing.T) {
	var d config.AgentDefaults
	d.Notify, d.QuietHours, d.NotifyDelay = "needs-you", "", 3*time.Minute
	d.Brief.Enabled, d.Brief.At = false, "08:00"
	on := true
	def := &Definition{QuietHours: "22:00-07:00", Brief: &BriefDef{Enabled: &on, At: "07:30"}}
	s := Resolve(def, d)
	if s.QuietHours != "22:00-07:00" || !s.BriefEnabled || s.BriefAt != "07:30" || s.NotifyDelay != 3*time.Minute || s.Notify != "needs-you" {
		t.Fatalf("settings: %+v", s)
	}
	dir := t.TempDir()
	if err := def.Save(dir); err != nil {
		t.Fatal(err)
	}
	back, err := LoadDefinition(dir)
	if err != nil || back.QuietHours != "22:00-07:00" || back.Brief == nil || back.Brief.At != "07:30" || !*back.Brief.Enabled {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}
