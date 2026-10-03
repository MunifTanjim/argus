package node

import (
	"context"
	"path/filepath"
	"strings"
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
	term := n.SessionTerminals["s2"]
	if term == "" {
		t.Fatal("SessionTerminals[s2] is empty, want resolved path")
	}
	if !filepath.IsAbs(term) {
		t.Fatalf("SessionTerminals[s2] = %q, want absolute path", term)
	}
	repo := n.Repos["s2"]
	if repo == "" {
		t.Fatal("Repos[s2] is empty, want resolved path")
	}
	if !filepath.IsAbs(repo) {
		t.Fatalf("Repos[s2] = %q, want absolute path", repo)
	}
}

func TestLoadDemoDataParsesProjectsAndTerminals(t *testing.T) {
	dd, err := LoadDemoData("testdata/demo_min.yaml")
	if err != nil {
		t.Fatalf("LoadDemoData: %v", err)
	}
	n := dd.Nodes[0]
	if len(n.Projects) != 2 || n.Projects[0].ID != "p1" || len(n.Projects[0].Workspaces) != 2 {
		t.Fatalf("projects = %+v", n.Projects)
	}
	if got := n.Projects[0].Workspaces[0].TargetBranch; got != "main" {
		t.Fatalf("w1 target = %q, want project default main", got)
	}
	if n.Projects[1].Workspaces == nil {
		t.Fatal("project without workspaces must have an empty slice, not nil")
	}
	if s := n.Projects[0].Workspaces[1].Setup; s == nil || s.State != "failed" || s.ExitCode != 2 {
		t.Fatalf("w2 setup = %+v", s)
	}
	if !strings.Contains(n.SetupLogs["w2"], "Error 2") {
		t.Fatalf("SetupLogs[w2] = %q", n.SetupLogs["w2"])
	}
	if n.WorkspaceRepos["w2"] == "" {
		t.Fatal("WorkspaceRepos[w2] is empty, want resolved path")
	}
	if _, ok := n.WorkspaceRepos["w1"]; ok {
		t.Fatal("w1 has no repo_spec")
	}
	if len(n.Terminals) != 2 || n.Terminals[0].ID != "@1" || !n.Terminals[0].Attached || n.Terminals[0].Name != "logs" {
		t.Fatalf("terminals = %+v", n.Terminals)
	}
	if n.NodeTerminals["@1"] == "" {
		t.Fatal("NodeTerminals[@1] is empty, want resolved path")
	}
	if _, ok := n.NodeTerminals["@2"]; ok {
		t.Fatal("@2 has no terminal_path")
	}
}

func TestLoadDemoDataRejectsDupTerminal(t *testing.T) {
	if _, err := LoadDemoData("testdata/demo_dup_terminal.yaml"); err == nil {
		t.Fatal("want error for duplicate terminal id")
	}
}

func TestLoadDemoDataRejectsDupWorkspace(t *testing.T) {
	if _, err := LoadDemoData("testdata/demo_dup_workspace.yaml"); err == nil {
		t.Fatal("want error for duplicate workspace id")
	}
}

func TestLoadDemoDataRejectsUnknownWorkspace(t *testing.T) {
	if _, err := LoadDemoData("testdata/demo_unknown_workspace.yaml"); err == nil {
		t.Fatal("want error for session workspace_id with no workspace")
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

func TestBuildDemoNodesSeedsRegistry(t *testing.T) {
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
	if n.e2ee {
		t.Fatal("demo nodes must use the plaintext relay uplink, not e2ee")
	}
	if n.identityPubB64 != "" {
		t.Fatal("plaintext demo nodes must not set a Noise identity")
	}
	if got := n.Registry().Snapshot(); len(got) != 2 {
		t.Fatalf("registry snapshot = %d, want 2", len(got))
	}
	if len(n.demoHistory) != 1 {
		t.Fatalf("demoHistory = %d, want 1", len(n.demoHistory))
	}
	if len(n.demoSessionTerminals) != 1 {
		t.Fatalf("demoSessionTerminals = %d, want 1", len(n.demoSessionTerminals))
	}
	if len(n.demoNodeTerminals) != 1 {
		t.Fatalf("demoNodeTerminals = %d, want 1", len(n.demoNodeTerminals))
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
	d.demoSessionTerminals = map[string][]byte{"s1": []byte("\x1b[32mhello\x1b[0m\n$ ")}

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

func TestBuildDemoNodesSetsCaps(t *testing.T) {
	dd, err := LoadDemoData("testdata/demo_min.yaml")
	if err != nil {
		t.Fatalf("LoadDemoData: %v", err)
	}
	dd.Nodes = append(dd.Nodes, DemoNode{ID: "bare", Label: "bare"})
	nodes, err := BuildDemoNodes(dd, "test")
	if err != nil {
		t.Fatalf("BuildDemoNodes: %v", err)
	}
	full, bare := nodes[0].caps, nodes[1].caps
	if !full.Terminal || !full.SpawnSession || full.HostWakelock {
		t.Fatalf("macbook caps = %+v, want terminal+spawn, no wakelock", full)
	}
	if bare.Terminal || bare.SpawnSession || bare.HostWakelock {
		t.Fatalf("bare caps = %+v, want none", bare)
	}
}

func TestDemoProjectAndTerminalHandlers(t *testing.T) {
	dd, err := LoadDemoData("testdata/demo_min.yaml")
	if err != nil {
		t.Fatalf("LoadDemoData: %v", err)
	}
	dd.Nodes = append(dd.Nodes, DemoNode{ID: "bare", Label: "bare"})
	nodes, err := BuildDemoNodes(dd, "test")
	if err != nil {
		t.Fatalf("BuildDemoNodes: %v", err)
	}
	ctx := context.Background()

	res, err := nodes[0].handleProjectList(ctx, nil)
	if err != nil {
		t.Fatalf("handleProjectList: %v", err)
	}
	if pl := res.(api.ProjectListResult); len(pl.Projects) != 2 || pl.Projects[0].ID != "p1" {
		t.Fatalf("projects = %+v", pl.Projects)
	}
	res, err = nodes[0].handleTerminalList(ctx, nil)
	if err != nil {
		t.Fatalf("handleTerminalList: %v", err)
	}
	if tl := res.(api.TerminalListResult); len(tl.Terminals) != 2 || tl.Terminals[0].ID != "@1" {
		t.Fatalf("terminals = %+v", tl.Terminals)
	}
	res, err = nodes[0].handleWorkspaceSetupLog(ctx, mustJSON(api.WorkspaceRef{WorkspaceID: "w2"}))
	if err != nil {
		t.Fatalf("handleWorkspaceSetupLog: %v", err)
	}
	if out := res.(api.SetupLogResult).Output; !strings.Contains(out, "Error 2") {
		t.Fatalf("setup log = %q", out)
	}

	res, _ = nodes[1].handleProjectList(ctx, nil)
	if pl := res.(api.ProjectListResult); pl.Projects == nil || len(pl.Projects) != 0 {
		t.Fatalf("bare projects = %#v, want empty non-nil", pl.Projects)
	}
	res, _ = nodes[1].handleTerminalList(ctx, nil)
	if tl := res.(api.TerminalListResult); tl.Terminals == nil || len(tl.Terminals) != 0 {
		t.Fatalf("bare terminals = %#v, want empty non-nil", tl.Terminals)
	}
}

func TestDemoTerminalOpenReplaysNodeTerminal(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.demo = true
	d.demoNodeTerminals = map[string][]byte{"@1": []byte("$ ls\r\n")}

	cn := &captureNotifier{}
	ctx := api.WithNotifier(context.Background(), cn)
	if _, err := d.handleTerminalOpen(ctx, mustJSON(api.TerminalOpenParams{TermID: "t1", TerminalID: "@1"})); err != nil {
		t.Fatalf("handleTerminalOpen: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for cn.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cn.count() == 0 {
		t.Fatal("no terminal.output frames for node terminal")
	}

	blank := &captureNotifier{}
	ctx = api.WithNotifier(context.Background(), blank)
	if _, err := d.handleTerminalOpen(ctx, mustJSON(api.TerminalOpenParams{TermID: "t2", TerminalID: "@9"})); err != nil {
		t.Fatalf("open terminal with no replay: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if blank.count() != 0 {
		t.Fatalf("frames for terminal with no replay = %d, want 0", blank.count())
	}
}

func TestDemoLaunchPaneFails(t *testing.T) {
	dd, err := LoadDemoData("testdata/demo_min.yaml")
	if err != nil {
		t.Fatalf("LoadDemoData: %v", err)
	}
	nodes, err := BuildDemoNodes(dd, "test")
	if err != nil {
		t.Fatalf("BuildDemoNodes: %v", err)
	}
	if _, err := nodes[0].launchPane(context.Background(), "", "sh", nil, t.TempDir()); err == nil {
		t.Fatal("launchPane on a demo node must fail")
	}
}

func TestDemoFleetFixtureLoads(t *testing.T) {
	dd, err := LoadDemoData("../../demo/fleet.yaml")
	if err != nil {
		t.Fatalf("LoadDemoData(demo/fleet.yaml): %v", err)
	}
	for _, n := range dd.Nodes {
		if len(n.Projects) == 0 || len(n.Terminals) == 0 {
			t.Errorf("node %s: projects=%d terminals=%d, want both", n.ID, len(n.Projects), len(n.Terminals))
		}
	}
}
