package node

import "testing"

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
	if len(n.Sessions) != 1 || n.Sessions[0].ID != "s1" {
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

func TestLoadDemoDataRejectsUnknownAgent(t *testing.T) {
	if _, err := LoadDemoData("testdata/demo_bad_agent.yaml"); err == nil {
		t.Fatal("want error for unknown agent")
	}
}
