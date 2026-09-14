package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	yaml "go.yaml.in/yaml/v3"

	"github.com/MunifTanjim/argus/internal/adapters"
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
// its past-sessions history, and per-session fixtures for the terminal and diff
// screens.
type DemoNode struct {
	ID        string
	Label     string
	Spawn     bool
	Sessions  []session.Session
	History   []DemoHistoryProject
	Terminals map[string]string // session id -> resolved terminal byte file
	Repos     map[string]string // session id -> resolved repo-spec directory
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

// LoadDemoData parses a demo fixture, resolving transcript_path, terminal_path,
// and repo_spec relative to the fixture directory, and validating id uniqueness
// and known agents.
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
		dn := DemoNode{ID: rn.ID, Label: rn.Label, Spawn: rn.Spawn, Terminals: map[string]string{}, Repos: map[string]string{}}
		for _, m := range rn.Sessions {
			termPath := strField(m, "terminal_path")
			repoSpec := strField(m, "repo_spec")
			b, err := json.Marshal(m)
			if err != nil {
				return nil, fmt.Errorf("encode session: %w", err)
			}
			var s session.Session
			if err := json.Unmarshal(b, &s); err != nil {
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
			if s.TranscriptPath, err = resolvePath(baseDir, s.TranscriptPath, s.ID); err != nil {
				return nil, err
			}
			if termPath != "" {
				rp, err := resolvePath(baseDir, termPath, s.ID+" terminal")
				if err != nil {
					return nil, err
				}
				dn.Terminals[s.ID] = rp
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
				b, err := json.Marshal(m)
				if err != nil {
					return nil, fmt.Errorf("encode history session: %w", err)
				}
				var hs session.HistorySession
				if err := json.Unmarshal(b, &hs); err != nil {
					return nil, fmt.Errorf("decode history session: %w", err)
				}
				if hs.Agent != "" && adapters.ByAgent(hs.Agent) == nil {
					return nil, fmt.Errorf("history session %s: unknown agent %q", hs.SessionID, hs.Agent)
				}
				if hs.TranscriptPath, err = resolvePath(baseDir, hs.TranscriptPath, hs.SessionID); err != nil {
					return nil, err
				}
				hp.Sessions = append(hp.Sessions, hs)
			}
			dn.History = append(dn.History, hp)
		}
		out.Nodes = append(out.Nodes, dn)
	}
	return out, nil
}

// BuildDemoNodes constructs one read-only demo node per fixture node: an empty
// tmux map (no spawn, no tmux), a seeded registry, and demo history and terminal
// fixtures. The nodes serve over the plaintext relay uplink (no e2ee), matching a
// gateway profile added without e2ee; the app opens plain channels to them. The
// nodes are ready to ConnectGateway; they must not Run.
func BuildDemoNodes(dd *DemoData, version string) ([]*Node, error) {
	out := make([]*Node, 0, len(dd.Nodes))
	for _, dn := range dd.Nodes {
		d := newNode(map[session.TmuxServer]*tmux.Client{})
		d.SetIdentity(dn.ID, dn.Label)
		d.SetVersion(version)

		d.demo = true
		d.demoHistory = dn.History
		d.reg.Seed(dn.Sessions)

		d.demoTerminals = map[string][]byte{}
		for sid, path := range dn.Terminals {
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("demo node %s terminal %s: %w", dn.ID, sid, err)
			}
			d.demoTerminals[sid] = b
		}
		out = append(out, d)
	}
	return out, nil
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
