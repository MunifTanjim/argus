package node

import (
	"path/filepath"
	"testing"
)

func TestLoadDemoDataParsesNodesAndHistory(t *testing.T) {
	dd, err := LoadDemoData("testdata/demo_min.yaml")
	if err != nil {
		t.Fatalf("LoadDemoData: %v", err)
	}
	if len(dd.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(dd.Nodes))
	}
	n := dd.Nodes[0]
	if n.ID != "macbook" || n.Label != "MacBook Pro" {
		t.Fatalf("node id/label = %q/%q", n.ID, n.Label)
	}
	if len(n.Sessions) != 2 || n.Sessions[0].ID != "s1" {
		t.Fatalf("sessions = %+v", n.Sessions)
	}
	if n.Sessions[0].TranscriptPath == "t.jsonl" {
		t.Fatal("transcript_path should be resolved to an absolute path")
	}
	projs := demoHistoryProjects(n.History)
	if len(projs) != 1 || projs[0].SessionCount != 1 {
		t.Fatalf("history projects = %+v", projs)
	}
	page := demoHistorySessions(n.History, "/Users/dev/argus", 0, 0)
	if len(page.Items) != 1 || page.Items[0].SessionID != "h1" {
		t.Fatalf("history sessions = %+v", page)
	}
}

func TestLoadDemoDataResolvesFixturePaths(t *testing.T) {
	abs, err := filepath.Abs("testdata/demo_min.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dd, err := LoadDemoData(abs)
	if err != nil {
		t.Fatalf("LoadDemoData: %v", err)
	}
	n := dd.Nodes[0]
	term := n.Terminals["s2"]
	if term == "" {
		t.Fatal("Terminals[s2] is empty, want resolved path")
	}
	if !filepath.IsAbs(term) {
		t.Fatalf("Terminals[s2] = %q, want absolute path", term)
	}
	repo := n.Repos["s2"]
	if repo == "" {
		t.Fatal("Repos[s2] is empty, want resolved path")
	}
	if !filepath.IsAbs(repo) {
		t.Fatalf("Repos[s2] = %q, want absolute path", repo)
	}
}

func TestLoadDemoDataRejectsUnknownAgent(t *testing.T) {
	if _, err := LoadDemoData("testdata/demo_bad_agent.yaml"); err == nil {
		t.Fatal("want error for unknown agent")
	}
}

func TestLoadDemoDataRejectsDupSession(t *testing.T) {
	if _, err := LoadDemoData("testdata/demo_dup_session.yaml"); err == nil {
		t.Fatal("want error for duplicate session id")
	}
}

func TestLoadDemoDataRejectsMissingFile(t *testing.T) {
	if _, err := LoadDemoData("testdata/demo_missing_transcript.yaml"); err == nil {
		t.Fatal("want error for missing transcript file")
	}
}
