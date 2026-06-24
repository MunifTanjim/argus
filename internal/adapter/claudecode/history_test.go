package claudecode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
	if len(projects) != 2 {
		t.Fatalf("want 2 projects, got %d: %+v", len(projects), projects)
	}
	// Newest-first: projB before projA.
	if projects[0].Label != "projB" || projects[1].Label != "projA" {
		t.Fatalf("project order/label wrong: %+v", projects)
	}
	if projects[1].SessionCount != 2 { // agent_ excluded
		t.Errorf("projA session count = %d, want 2", projects[1].SessionCount)
	}
	if projects[1].Cwd != "/work/projA" {
		t.Errorf("projA cwd = %q, want /work/projA", projects[1].Cwd)
	}

	// First page of projA: newest (s2) first, more remaining.
	page, err := ListHistorySessions(projects[1].ProjectDir, 1, 0)
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
	page2, err := ListHistorySessions(projects[1].ProjectDir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 1 || page2.HasMore || page2.Items[0].SessionID != "s1" {
		t.Fatalf("page 1: items=%+v hasMore=%v", page2.Items, page2.HasMore)
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
