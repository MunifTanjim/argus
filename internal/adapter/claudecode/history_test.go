package claudecode

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/session"
)

// writeTranscript writes a minimal one-turn transcript (a single user message with
// a cwd), enough for DiscoverProjectSessions to count it as a real session.
func writeTranscript(t *testing.T, path, cwd string, mod time.Time) {
	t.Helper()
	line := `{"uuid":"u1","type":"user","timestamp":"2025-01-15T10:00:00Z","isSidechain":false,"isMeta":false,"cwd":"` +
		cwd + `","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestListHistoryProjectsAndSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	config.CacheDir = t.TempDir()
	root := filepath.Join(home, ".claude", "projects")

	projA := filepath.Join(root, "-work-projA")
	projB := filepath.Join(root, "-work-projB")
	for _, d := range []string{projA, projB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	// projA: two sessions, s2 newer than s1. projB: one session, newest overall.
	writeTranscript(t, filepath.Join(projA, "s1.jsonl"), "/work/projA", now.Add(-3*time.Hour))
	writeTranscript(t, filepath.Join(projA, "s2.jsonl"), "/work/projA", now.Add(-2*time.Hour))
	writeTranscript(t, filepath.Join(projB, "b1.jsonl"), "/work/projB", now.Add(-1*time.Hour))
	// A subagent file must not be counted.
	writeTranscript(t, filepath.Join(projA, "agent_x.jsonl"), "/work/projA", now)

	projects, err := ListHistoryProjects()
	if err != nil {
		t.Fatal(err)
	}
	byCwd := map[string]session.HistoryProject{}
	for _, p := range projects {
		byCwd[p.ProjectDir] = p // ProjectDir is now the cwd
	}
	if _, ok := byCwd["/work/projA"]; !ok {
		t.Fatalf("expected project keyed by cwd /work/projA, got %+v", projects)
	}
	if byCwd["/work/projA"].SessionCount != 2 {
		t.Errorf("projA session count = %d, want 2", byCwd["/work/projA"].SessionCount)
	}

	page, err := ListHistorySessions("/work/projA", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("ListHistorySessions(/work/projA) = %d items, want 2", len(page.Items))
	}

	// First page of projA: newest (s2) first, more remaining.
	page, err = ListHistorySessions("/work/projA", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.HasMore {
		t.Fatalf("page 0: items=%d hasMore=%v", len(page.Items), page.HasMore)
	}
	if page.Items[0].SessionID != "s2" {
		t.Errorf("newest session = %q, want s2", page.Items[0].SessionID)
	}
	// Second page: the older session, nothing more.
	page2, err := ListHistorySessions("/work/projA", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 1 || page2.HasMore || page2.Items[0].SessionID != "s1" {
		t.Fatalf("page 1: items=%+v hasMore=%v", page2.Items, page2.HasMore)
	}
}

// writeTranscriptMsg writes a one-turn transcript with an arbitrary user message,
// used to prove the cache serves stale content until the mod time changes.
func writeTranscriptMsg(t *testing.T, path, cwd, msg string, mod time.Time) {
	t.Helper()
	line := `{"uuid":"u1","type":"user","timestamp":"2025-01-15T10:00:00Z","isSidechain":false,"isMeta":false,"cwd":"` +
		cwd + `","message":{"role":"user","content":"` + msg + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func firstMessage(t *testing.T, cwd string) string {
	t.Helper()
	page, err := ListHistorySessions(cwd, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 session, got %d", len(page.Items))
	}
	return page.Items[0].FirstMessage
}

func TestClaudeHistorySessionsCacheKeyedOnModTime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	config.CacheDir = t.TempDir()
	proj := filepath.Join(home, ".claude", "projects", "-work-p")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "s1.jsonl")
	mod := time.Now().Add(-time.Hour)
	// "hello" and "world" are equal length, so the rewrite keeps the file size.
	writeTranscriptMsg(t, path, "/work/p", "hello", mod)

	if got := firstMessage(t, "/work/p"); got != "hello" { // warm the cache
		t.Fatalf("first list first_message = %q, want hello", got)
	}

	// Same mod time + size ⇒ cache hit, the rewritten content is ignored.
	writeTranscriptMsg(t, path, "/work/p", "world", mod)
	if got := firstMessage(t, "/work/p"); got != "hello" {
		t.Fatalf("cached first_message = %q, want hello", got)
	}

	// Bump mod time ⇒ cache miss ⇒ rescan picks up the new content.
	newMod := time.Now()
	if err := os.Chtimes(path, newMod, newMod); err != nil {
		t.Fatal(err)
	}
	if got := firstMessage(t, "/work/p"); got != "world" {
		t.Fatalf("rescanned first_message = %q, want world", got)
	}
}

func TestReadHistoryTranscriptRejectsOutsideRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".claude", "projects", "-work-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(root, "s.jsonl")
	writeTranscript(t, good, "/work/p", time.Now())

	if _, err := ReadHistoryTranscript(good); err != nil {
		t.Errorf("valid transcript should read: %v", err)
	}
	if _, err := ReadHistoryTranscript("/etc/passwd"); err == nil {
		t.Error("a path outside the projects root must be rejected")
	}
}
