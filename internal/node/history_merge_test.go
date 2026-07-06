package node

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestMergeProjectsByCwd(t *testing.T) {
	a := []session.HistoryProject{{ProjectDir: "/w/x", Cwd: "/w/x", Label: "x", SessionCount: 2, LastActivity: "2026-01-02T00:00:00Z"}}
	b := []session.HistoryProject{
		{ProjectDir: "/w/x", Cwd: "/w/x", Label: "x", SessionCount: 3, LastActivity: "2026-01-03T00:00:00Z"},
		{ProjectDir: "/w/y", Cwd: "/w/y", Label: "y", SessionCount: 1, LastActivity: "2026-01-01T00:00:00Z"},
	}
	got := mergeProjects([][]session.HistoryProject{a, b})
	if len(got) != 2 {
		t.Fatalf("merged projects = %d, want 2", len(got))
	}
	if got[0].ProjectDir != "/w/x" { // newest activity first
		t.Errorf("first project = %q, want /w/x", got[0].ProjectDir)
	}
	if got[0].SessionCount != 5 {
		t.Errorf("merged /w/x count = %d, want 5", got[0].SessionCount)
	}
	if got[0].LastActivity != "2026-01-03T00:00:00Z" {
		t.Errorf("merged /w/x last activity = %q, want max", got[0].LastActivity)
	}
}

func TestMergeSessionsSortsAndPaginates(t *testing.T) {
	items := []session.HistorySession{
		{SessionID: "a", Agent: "claude", LastActivity: "2026-01-01T00:00:00Z"},
		{SessionID: "b", Agent: "codex", LastActivity: "2026-01-03T00:00:00Z"},
		{SessionID: "c", Agent: "antigravity", LastActivity: "2026-01-02T00:00:00Z"},
	}
	page := mergeSessions(items, 0, 2)
	if len(page.Items) != 2 || !page.HasMore {
		t.Fatalf("page = %+v, want 2 items + HasMore", page)
	}
	if page.Items[0].SessionID != "b" || page.Items[1].SessionID != "c" {
		t.Errorf("order = %q,%q; want b,c (newest first)", page.Items[0].SessionID, page.Items[1].SessionID)
	}
	last := mergeSessions(items, 2, 2)
	if len(last.Items) != 1 || last.HasMore {
		t.Errorf("last page = %+v, want 1 item, no more", last)
	}
}
