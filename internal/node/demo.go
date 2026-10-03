package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	yaml "go.yaml.in/yaml/v3"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// DemoHistoryProject is one past-sessions project for the History view.
type DemoHistoryProject struct {
	Repo     string
	Cwd      string
	Sessions []session.HistorySession
}

// DemoNode is one fake node in a demo fixture: an identity, its live sessions,
// its past-sessions history, its project tree and terminals, and the fixture
// files behind the terminal and diff screens.
type DemoNode struct {
	ID               string
	Label            string
	Spawn            bool
	Sessions         []session.Session
	History          []DemoHistoryProject
	Projects         []api.ProjectNode
	Terminals        []api.Terminal
	SessionTerminals map[string]string // session id -> resolved terminal byte file
	NodeTerminals    map[string]string // terminal id -> resolved terminal byte file
	Repos            map[string]string // session id -> resolved repo-spec directory
	WorkspaceRepos   map[string]string // workspace id -> resolved repo-spec directory
	SetupLogs        map[string]string // workspace id -> setup log text
}

// DemoData is a parsed --demo-data fixture.
type DemoData struct {
	Nodes []DemoNode
}

type rawDemo struct {
	Nodes []struct {
		ID       string           `yaml:"id"`
		Label    string           `yaml:"label"`
		Spawn    bool             `yaml:"spawn"`
		Sessions []map[string]any `yaml:"sessions"`
		History  []struct {
			Repo     string           `yaml:"repo"`
			Cwd      string           `yaml:"cwd"`
			Sessions []map[string]any `yaml:"sessions"`
		} `yaml:"history"`
		Projects  []map[string]any `yaml:"projects"`
		Terminals []map[string]any `yaml:"terminals"`
	} `yaml:"nodes"`
}

func resolvePath(baseDir, p, who string) (string, error) {
	if p == "" {
		return "", nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("fixture path for %s: %w", who, err)
	}
	return p, nil
}

func strField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func decodeVia(m map[string]any, v any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

type demoWorkspaceExtras struct {
	Workspaces []struct {
		ID       string `json:"id"`
		SetupLog string `json:"setup_log"`
		RepoSpec string `json:"repo_spec"`
	} `json:"workspaces"`
}

func parseDemoProjects(baseDir string, dn *DemoNode, raws []map[string]any) error {
	seenProj := map[string]bool{}
	seenWs := map[string]bool{}
	for _, m := range raws {
		var p api.ProjectNode
		if err := decodeVia(m, &p); err != nil {
			return fmt.Errorf("decode project: %w", err)
		}
		if p.ID == "" {
			return fmt.Errorf("demo node %s: project missing id", dn.ID)
		}
		if seenProj[p.ID] {
			return fmt.Errorf("demo node %s: duplicate project id: %s", dn.ID, p.ID)
		}
		seenProj[p.ID] = true
		if p.Workspaces == nil {
			p.Workspaces = []api.WorkspaceNode{}
		}
		for i := range p.Workspaces {
			if p.Workspaces[i].TargetBranch == "" {
				p.Workspaces[i].TargetBranch = p.DefaultBranch
			}
		}
		var extras demoWorkspaceExtras
		if err := decodeVia(m, &extras); err != nil {
			return fmt.Errorf("decode project %s workspaces: %w", p.ID, err)
		}
		for _, w := range extras.Workspaces {
			if w.ID == "" {
				return fmt.Errorf("demo project %s: workspace missing id", p.ID)
			}
			if seenWs[w.ID] {
				return fmt.Errorf("demo node %s: duplicate workspace id: %s", dn.ID, w.ID)
			}
			seenWs[w.ID] = true
			if w.SetupLog != "" {
				dn.SetupLogs[w.ID] = w.SetupLog
			}
			if w.RepoSpec != "" {
				rp, err := resolvePath(baseDir, w.RepoSpec, w.ID+" repo")
				if err != nil {
					return err
				}
				dn.WorkspaceRepos[w.ID] = rp
			}
		}
		dn.Projects = append(dn.Projects, p)
	}
	for _, s := range dn.Sessions {
		if s.WorkspaceID != "" && !seenWs[s.WorkspaceID] {
			return fmt.Errorf("session %s: unknown workspace %q on node %s", s.ID, s.WorkspaceID, dn.ID)
		}
	}
	return nil
}

func parseDemoTerminals(baseDir string, dn *DemoNode, raws []map[string]any) error {
	seen := map[string]bool{}
	for _, m := range raws {
		var t api.Terminal
		if err := decodeVia(m, &t); err != nil {
			return fmt.Errorf("decode terminal: %w", err)
		}
		if t.ID == "" {
			return fmt.Errorf("demo node %s: terminal missing id", dn.ID)
		}
		if seen[t.ID] {
			return fmt.Errorf("demo node %s: duplicate terminal id: %s", dn.ID, t.ID)
		}
		seen[t.ID] = true
		if p := strField(m, "terminal_path"); p != "" {
			rp, err := resolvePath(baseDir, p, "terminal "+t.ID)
			if err != nil {
				return err
			}
			dn.NodeTerminals[t.ID] = rp
		}
		dn.Terminals = append(dn.Terminals, t)
	}
	return nil
}

// LoadDemoData parses a demo fixture, resolving transcript_path, terminal_path,
// and repo_spec (of sessions, terminals, and workspaces) relative to the fixture
// directory, and validating id uniqueness, known agents, and session workspaces.
func LoadDemoData(path string) (*DemoData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawDemo
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse demo data: %w", err)
	}
	baseDir := filepath.Dir(path)
	seen := map[string]bool{}
	seenNodes := map[string]bool{}
	out := &DemoData{}
	for _, rn := range raw.Nodes {
		if rn.ID == "" {
			return nil, fmt.Errorf("demo node missing id")
		}
		if rn.Label == "" {
			return nil, fmt.Errorf("demo node %s missing label", rn.ID)
		}
		if seenNodes[rn.ID] {
			return nil, fmt.Errorf("duplicate demo node id: %s", rn.ID)
		}
		seenNodes[rn.ID] = true
		dn := DemoNode{
			ID: rn.ID, Label: rn.Label, Spawn: rn.Spawn,
			SessionTerminals: map[string]string{}, NodeTerminals: map[string]string{},
			Repos: map[string]string{}, WorkspaceRepos: map[string]string{}, SetupLogs: map[string]string{},
		}
		for _, m := range rn.Sessions {
			termPath := strField(m, "terminal_path")
			repoSpec := strField(m, "repo_spec")
			var s session.Session
			if err := decodeVia(m, &s); err != nil {
				return nil, fmt.Errorf("decode session: %w", err)
			}
			if s.ID == "" {
				return nil, fmt.Errorf("demo session missing id")
			}
			if adapters.ByAgent(s.Agent) == nil {
				return nil, fmt.Errorf("session %s: unknown agent %q", s.ID, s.Agent)
			}
			if seen[s.ID] {
				return nil, fmt.Errorf("duplicate demo session id: %s", s.ID)
			}
			seen[s.ID] = true
			var err error
			if s.TranscriptPath, err = resolvePath(baseDir, s.TranscriptPath, s.ID); err != nil {
				return nil, err
			}
			if termPath != "" {
				rp, err := resolvePath(baseDir, termPath, s.ID+" terminal")
				if err != nil {
					return nil, err
				}
				dn.SessionTerminals[s.ID] = rp
			}
			if repoSpec != "" {
				rp, err := resolvePath(baseDir, repoSpec, s.ID+" repo")
				if err != nil {
					return nil, err
				}
				dn.Repos[s.ID] = rp
			}
			dn.Sessions = append(dn.Sessions, s)
		}
		for _, rp := range rn.History {
			hp := DemoHistoryProject{Repo: rp.Repo, Cwd: rp.Cwd}
			for _, m := range rp.Sessions {
				var hs session.HistorySession
				if err := decodeVia(m, &hs); err != nil {
					return nil, fmt.Errorf("decode history session: %w", err)
				}
				if hs.Agent != "" && adapters.ByAgent(hs.Agent) == nil {
					return nil, fmt.Errorf("history session %s: unknown agent %q", hs.SessionID, hs.Agent)
				}
				var err error
				if hs.TranscriptPath, err = resolvePath(baseDir, hs.TranscriptPath, hs.SessionID); err != nil {
					return nil, err
				}
				hp.Sessions = append(hp.Sessions, hs)
			}
			dn.History = append(dn.History, hp)
		}
		if err := parseDemoProjects(baseDir, &dn, rn.Projects); err != nil {
			return nil, err
		}
		if err := parseDemoTerminals(baseDir, &dn, rn.Terminals); err != nil {
			return nil, err
		}
		out.Nodes = append(out.Nodes, dn)
	}
	return out, nil
}

func readDemoTerminals(nodeID string, paths map[string]string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(paths))
	for id, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("demo node %s terminal %s: %w", nodeID, id, err)
		}
		out[id] = b
	}
	return out, nil
}

// BuildDemoNodes constructs one read-only demo node per fixture node: an empty
// tmux map, a seeded registry, and the fixture's history, projects, terminals,
// and replay bytes. Capabilities follow the fixture (terminals present, spawn),
// but spawn and resume still fail without tmux, and the wakelock is always off
// so a demo never holds the host awake. The nodes serve over the plaintext relay
// uplink (no e2ee), matching a gateway profile added without e2ee; the app opens
// plain channels to them. The nodes are ready to ConnectGateway; they must not
// Run.
func BuildDemoNodes(dd *DemoData, version string) ([]*Node, error) {
	out := make([]*Node, 0, len(dd.Nodes))
	for _, dn := range dd.Nodes {
		d := newNode(map[session.TmuxServer]*tmux.Client{})
		d.SetIdentity(dn.ID, dn.Label)
		d.SetVersion(version)
		d.caps.Terminal = len(dn.Terminals) > 0
		d.caps.SpawnSession = dn.Spawn
		d.caps.HostWakelock = false

		d.demo = true
		d.demoHistory = dn.History
		d.demoProjects = dn.Projects
		d.demoTerminalList = dn.Terminals
		d.demoSetupLogs = dn.SetupLogs
		d.demoWorkspaceDirs = dn.WorkspaceRepos
		d.reg.Seed(dn.Sessions)

		var err error
		if d.demoSessionTerminals, err = readDemoTerminals(dn.ID, dn.SessionTerminals); err != nil {
			return nil, err
		}
		if d.demoNodeTerminals, err = readDemoTerminals(dn.ID, dn.NodeTerminals); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (d *Node) demoTargetBranch(wsID string) string {
	for _, p := range d.demoProjects {
		for _, w := range p.Workspaces {
			if w.ID == wsID {
				return w.TargetBranch
			}
		}
	}
	return ""
}

func demoHistoryProjects(hist []DemoHistoryProject) []session.HistoryProject {
	out := make([]session.HistoryProject, 0, len(hist))
	for _, p := range hist {
		label := p.Repo
		if label == "" {
			label = filepath.Base(p.Cwd)
		}
		last := ""
		for _, s := range p.Sessions {
			if s.LastActivity > last {
				last = s.LastActivity
			}
		}
		out = append(out, session.HistoryProject{
			ProjectDir:   p.Cwd,
			Cwd:          p.Cwd,
			Repo:         p.Repo,
			Label:        label,
			SessionCount: len(p.Sessions),
			LastActivity: last,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActivity > out[j].LastActivity })
	return out
}

func demoHistorySessions(hist []DemoHistoryProject, projectDir string, offset, limit int) session.HistorySessionPage {
	var items []session.HistorySession
	for _, p := range hist {
		if p.Cwd == projectDir {
			items = append(items, p.Sessions...)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].LastActivity > items[j].LastActivity })
	if offset > len(items) {
		offset = len(items)
	}
	items = items[offset:]
	hasMore := false
	if limit > 0 && limit < len(items) {
		items = items[:limit]
		hasMore = true
	}
	return session.HistorySessionPage{Items: items, HasMore: hasMore}
}
