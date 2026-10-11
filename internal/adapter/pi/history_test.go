package pi

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const histFixtureA = `{"type":"session","version":3,"id":"aaa","timestamp":"2026-10-11T18:00:00.000Z","cwd":"/work/proj"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-11T18:00:01.000Z","message":{"role":"user","content":"first task here","timestamp":1}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-11T18:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"provider":"p","model":"model-a","usage":{"input":100,"output":50,"reasoning":20,"totalTokens":150},"stopReason":"stop","timestamp":2}}
{"type":"message","id":"t1","parentId":"m2","timestamp":"2026-10-11T18:00:02.500Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"x","content":[],"usage":{"input":7,"output":3,"totalTokens":10},"isError":false,"timestamp":3}}
{"type":"session_info","id":"s1","parentId":"t1","timestamp":"2026-10-11T18:00:03.000Z","name":"Alpha"}
{"type":"compaction","id":"c1","parentId":"s1","timestamp":"2026-10-11T18:00:04.000Z","summary":"s","firstKeptEntryId":"m1","usage":{"input":10,"output":5,"totalTokens":15}}
{"type":"branch_summary","id":"b1","parentId":"c1","timestamp":"2026-10-11T18:00:05.000Z","summary":"s","usage":{"input":4,"output":2,"totalTokens":6}}
{"type":"usage","id":"u1","parentId":"b1","timestamp":"2026-10-11T18:00:06.000Z","kind":"cache_warm","usage":{"input":1,"output":1,"cacheRead":500,"totalTokens":502}}
`

const histFixtureB = `{"type":"session","version":3,"id":"bbb","timestamp":"2026-10-11T18:00:00.000Z","cwd":"/work/other"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-11T18:00:01.000Z","message":{"role":"user","content":"other work","timestamp":1}}
`

func writeHistFixture(t *testing.T, root, group, file, content string, modtime time.Time) string {
	t.Helper()
	dir := filepath.Join(root, group)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, file)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, modtime, modtime); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListHistory(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	a := writeHistFixture(t, root, "--work-proj--", "2026-10-11T18-00-00-000Z_aaa.jsonl", histFixtureA, now)
	b := writeHistFixture(t, root, "--work-other--", "2026-10-11T18-00-00-000Z_bbb.jsonl", histFixtureB, now.Add(-time.Hour))
	_ = a
	_ = b

	projects, err := listHistoryProjectsIn(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("projects = %+v", projects)
	}
	byDir := map[string]int{}
	for _, p := range projects {
		byDir[p.ProjectDir] = p.SessionCount
	}
	if byDir["/work/proj"] != 1 || byDir["/work/other"] != 1 {
		t.Fatalf("projects = %+v", projects)
	}

	page, err := listHistorySessionsIn(root, "/work/proj", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.HasMore {
		t.Fatalf("page = %+v", page)
	}
	it := page.Items[0]
	if it.SessionID != "aaa" || it.Agent != Agent {
		t.Fatalf("item = %+v", it)
	}
	if it.Title != "Alpha" { // session_info name wins over first message
		t.Fatalf("title = %q", it.Title)
	}
	// Reasoning is part of output; tool results, summaries and usage entries
	// count too.
	if it.ModelName != "model-a" || it.Tokens != 183 {
		t.Fatalf("item = %+v", it)
	}
	if it.TranscriptPath != a {
		t.Fatalf("transcript path = %q", it.TranscriptPath)
	}

	// Pagination.
	page, err = listHistorySessionsIn(root, "/work/proj", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.HasMore {
		t.Fatalf("page = %+v", page)
	}
}

func TestCollectSessionFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_HOME", home)
	root := filepath.Join(home, "agent", "sessions")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", root)
	p := writeHistFixture(t, root, "--work-proj--", "2026-10-11T18-00-00-000Z_aaa.jsonl", histFixtureA, time.Now())
	files, err := collectSessionFiles(p)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	if _, err := collectSessionFiles(filepath.Join(root, "nope.jsonl")); err == nil {
		t.Fatal("expected error for missing file")
	}
	if _, err := collectSessionFiles("/etc/hostname"); err == nil {
		t.Fatal("expected error outside sessions root")
	}
}

func TestReadHistoryTranscript(t *testing.T) {
	root := t.TempDir()
	p := writeHistFixture(t, root, "--work-proj--", "2026-10-11T18-00-00-000Z_aaa.jsonl", histFixtureA, time.Now())
	view, err := readTranscriptView(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) == 0 {
		t.Fatal("empty view")
	}
	if view.Entries[0].Kind != "user" || view.Entries[0].Text != "first task here" {
		t.Fatalf("first = %+v", view.Entries[0])
	}
}

func TestStreamingRefresh(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(histFixtureB), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newStreamingTranscript(p)
	first, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("first = %d", len(first))
	}
	// Unchanged file → same entries.
	second, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("second = %d", len(second))
	}
	// Appended message → growth noticed. mtime granularity is coarse on some
	// filesystems, so the size change must catch it regardless.
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{\"type\":\"message\",\"id\":\"m2\",\"parentId\":\"m1\",\"timestamp\":\"2026-10-11T18:00:02.000Z\",\"message\":{\"role\":\"user\",\"content\":\"more\",\"timestamp\":2}}\n")
	f.Close()
	third, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 2 {
		t.Fatalf("third = %d", len(third))
	}
	// Missing file → last good.
	missing := newStreamingTranscript(filepath.Join(t.TempDir(), "no.jsonl"))
	entries, err := missing.Refresh()
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing = %v %v", entries, err)
	}
}
