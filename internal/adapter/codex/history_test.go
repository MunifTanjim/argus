package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/session"
)

func writeRollout(t *testing.T, home, id, cwd, model string, mod time.Time) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "01", "15")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-01-15T10-00-00-"+id+".jsonl")
	lines := `{"timestamp":"2026-01-15T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-01-15T10:00:01Z","type":"turn_context","payload":{"model":"` + model + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodexHistoryProjectsAndSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	config.CacheDir = t.TempDir()
	now := time.Now()
	writeRollout(t, home, "t1", "/work/proj", "gpt-5-codex", now.Add(-2*time.Hour))
	writeRollout(t, home, "t2", "/work/proj", "gpt-5-codex", now.Add(-1*time.Hour))
	writeRollout(t, home, "t3", "/work/other", "gpt-5-codex", now.Add(-3*time.Hour))

	projects, err := ListHistoryProjects()
	if err != nil {
		t.Fatal(err)
	}
	byCwd := map[string]int{}
	for _, p := range projects {
		byCwd[p.ProjectDir] = p.SessionCount
	}
	if byCwd["/work/proj"] != 2 {
		t.Errorf("/work/proj count = %d, want 2 (projects=%+v)", byCwd["/work/proj"], projects)
	}
	if byCwd["/work/other"] != 1 {
		t.Errorf("/work/other count = %d, want 1", byCwd["/work/other"])
	}

	page, err := ListHistorySessions("/work/proj", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("sessions = %d, want 2", len(page.Items))
	}
	if page.Items[0].ModelName == "" || page.Items[0].TranscriptPath == "" {
		t.Errorf("session missing model/path: %+v", page.Items[0])
	}
}

// rewriteRolloutCwd overwrites a rollout with a new (equal-length) cwd, keeping
// its byte size, then restores mod time to mod.
func rewriteRolloutCwd(t *testing.T, path, id, cwd, model string, mod time.Time) {
	t.Helper()
	lines := `{"timestamp":"2026-01-15T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-01-15T10:00:01Z","type":"turn_context","payload":{"model":"` + model + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestCodexHistoryCacheKeyedOnModTime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	config.CacheDir = t.TempDir()
	mod := time.Now().Add(-time.Hour)
	// "/work/aaa" and "/work/bbb" are equal length: rewrites keep the file size.
	path := writeRollout(t, home, "t1", "/work/aaa", "gpt-5-codex", mod)

	if _, err := ListHistoryProjects(); err != nil { // warm the cache
		t.Fatal(err)
	}

	// Same mod time + size ⇒ cache hit, the rewritten cwd is ignored.
	rewriteRolloutCwd(t, path, "t1", "/work/bbb", "gpt-5-codex", mod)
	projects, err := ListHistoryProjects()
	if err != nil {
		t.Fatal(err)
	}
	if !hasProject(projects, "/work/aaa") || hasProject(projects, "/work/bbb") {
		t.Fatalf("expected cached /work/aaa, got %+v", projects)
	}

	// Bump mod time ⇒ cache miss ⇒ rescan picks up the new cwd.
	newMod := time.Now()
	if err := os.Chtimes(path, newMod, newMod); err != nil {
		t.Fatal(err)
	}
	projects, err = ListHistoryProjects()
	if err != nil {
		t.Fatal(err)
	}
	if !hasProject(projects, "/work/bbb") || hasProject(projects, "/work/aaa") {
		t.Fatalf("expected rescanned /work/bbb, got %+v", projects)
	}
}

func hasProject(ps []session.HistoryProject, cwd string) bool {
	for _, p := range ps {
		if p.ProjectDir == cwd {
			return true
		}
	}
	return false
}

func TestCodexHistoryTranscriptPathGuard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if _, err := ReadHistoryTranscript("/etc/passwd"); err == nil {
		t.Error("expected error reading path outside codex sessions root")
	}
}
