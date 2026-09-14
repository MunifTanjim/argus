package node

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
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

func TestBuildDemoNodesSeedsRegistryAndIdentity(t *testing.T) {
	dd, err := LoadDemoData("testdata/demo_min.yaml")
	if err != nil {
		t.Fatalf("LoadDemoData: %v", err)
	}
	nodes, err := BuildDemoNodes(dd, "test")
	if err != nil {
		t.Fatalf("BuildDemoNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	n := nodes[0]
	if n.id != "macbook" {
		t.Fatalf("id = %q, want macbook", n.id)
	}
	if !n.demo {
		t.Fatal("demo flag not set")
	}
	if n.identityPubB64 == "" {
		t.Fatal("ephemeral identity not set")
	}
	if got := n.Registry().Snapshot(); len(got) != 2 {
		t.Fatalf("registry snapshot = %d, want 2", len(got))
	}
	if len(n.demoHistory) != 1 {
		t.Fatalf("demoHistory = %d, want 1", len(n.demoHistory))
	}
	if !n.e2ee {
		t.Fatal("e2ee not set")
	}
	if len(n.demoTerminals) != 1 {
		t.Fatalf("demoTerminals = %d, want 1", len(n.demoTerminals))
	}
}

func TestHistoryHandlersServeDemoFixtures(t *testing.T) {
	dd, _ := LoadDemoData("testdata/demo_min.yaml")
	nodes, _ := BuildDemoNodes(dd, "test")
	d := nodes[0]

	res, err := d.handleHistoryProjects(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleHistoryProjects: %v", err)
	}
	projs, ok := res.([]session.HistoryProject)
	if !ok || len(projs) != 1 {
		t.Fatalf("projects = %#v", res)
	}
}

type captureNotifier struct {
	mu      sync.Mutex
	methods []string
}

func (c *captureNotifier) Notify(method string, _ any) error {
	c.mu.Lock()
	c.methods = append(c.methods, method)
	c.mu.Unlock()
	return nil
}
func (c *captureNotifier) count() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.methods) }

func TestDemoTerminalOpenEmitsOutput(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.demo = true
	d.demoTerminals = map[string][]byte{"s1": []byte("\x1b[32mhello\x1b[0m\n$ ")}

	cn := &captureNotifier{}
	ctx := api.WithNotifier(context.Background(), cn)
	if _, err := d.handleTerminalOpen(ctx, mustJSON(api.TerminalOpenParams{TermID: "t1", SessionID: "s1"})); err != nil {
		t.Fatalf("handleTerminalOpen: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for cn.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cn.count() == 0 {
		t.Fatal("no terminal.output frames emitted")
	}
	cn.mu.Lock()
	first := cn.methods[0]
	cn.mu.Unlock()
	if first != api.MethodTerminalOutput {
		t.Fatalf("first method = %q, want %q", first, api.MethodTerminalOutput)
	}
}
