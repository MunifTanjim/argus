package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bundle"
	"github.com/MunifTanjim/argus/internal/session"
)

// fileClient is an offline tui.Client backed by an extracted .argus bundle. It
// answers the read-only history RPCs from disk and no-ops everything live.
type fileClient struct {
	destDir   string
	manifest  bundle.Manifest
	adapter   adapter.Adapter
	entryPath string // absolute path to the main transcript file
	events    chan api.Notification
	states    chan bool
	closeOnce sync.Once
}

func newFileClient(destDir string, m bundle.Manifest) (*fileClient, error) {
	a := adapters.ByAgent(m.Agent)
	if a == nil {
		return nil, fmt.Errorf("view: unknown agent %q in bundle", m.Agent)
	}
	return &fileClient{
		destDir:   destDir,
		manifest:  m,
		adapter:   a,
		entryPath: filepath.Join(destDir, filepath.FromSlash(m.Entry)),
		events:    make(chan api.Notification),
		states:    make(chan bool),
	}, nil
}

func (c *fileClient) syntheticProject() session.HistoryProject {
	md := c.manifest.Metadata
	label := md.Repo
	if label == "" {
		label = md.Title
	}
	if label == "" {
		label = filepath.Base(md.Cwd)
	}
	return session.HistoryProject{
		ProjectDir:   c.destDir,
		Cwd:          md.Cwd,
		Repo:         md.Repo,
		Label:        label,
		SessionCount: 1,
		LastActivity: md.LastActivity,
	}
}

func (c *fileClient) syntheticSession() session.HistorySession {
	md := c.manifest.Metadata
	return session.HistorySession{
		SessionID:      "exported",
		Agent:          c.manifest.Agent,
		Title:          md.Title,
		FirstMessage:   md.FirstMessage,
		TranscriptPath: c.entryPath,
		ModelName:      md.ModelName,
		ModelColor:     md.ModelColor,
		LastActivity:   md.LastActivity,
		Tokens:         md.Tokens,
		TurnCount:      md.TurnCount,
		DurationMs:     md.DurationMs,
	}
}

func (c *fileClient) Call(method string, params, out any) error {
	switch method {
	case api.MethodServerInfo:
		return assign(out, api.ServerInfo{Version: c.manifest.ArgusVersion})
	case api.MethodSessionsList:
		return assign(out, []session.Session{})
	case api.MethodSessionsHistoryProjects:
		return assign(out, []session.HistoryProject{c.syntheticProject()})
	case api.MethodSessionsHistorySessions:
		return assign(out, session.HistorySessionPage{Items: []session.HistorySession{c.syntheticSession()}})
	case api.MethodSessionsHistoryTranscript:
		p, err := decodeParams[api.HistoryTranscriptParams](params)
		if err != nil {
			return err
		}
		if p.AgentID != "" {
			v, _, err := c.adapter.ReadSubagentView(c.entryPath, p.AgentID)
			if err != nil {
				return err
			}
			return assign(out, v)
		}
		v, err := c.adapter.ReadTranscriptView(c.entryPath)
		if err != nil {
			return err
		}
		return assign(out, v)
	case api.MethodSessionHistoryToolDetail:
		p, err := decodeParams[api.HistoryToolDetailParams](params)
		if err != nil {
			return err
		}
		td, _, err := c.adapter.FindToolDetail(c.entryPath, p.AgentID, p.ToolID)
		if err != nil {
			return err
		}
		return assign(out, td)
	default:
		return nil // live-only methods are no-ops offline
	}
}

func (c *fileClient) Events() <-chan api.Notification { return c.events }
func (c *fileClient) States() <-chan bool             { return c.states }
func (c *fileClient) Reconnect()                      {}

// Close releases the client. The extracted destDir is left in place: it's a
// deterministic per-bundle cache (see RunBundle) reused on the next open.
func (c *fileClient) Close() error {
	c.closeOnce.Do(func() { close(c.events) })
	return nil
}

// assign marshals v then unmarshals into out, mirroring how ReconnectingClient.Call
// populates the out pointer via JSON-RPC decoding.
func assign(out, v any) error {
	if out == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func decodeParams[T any](params any) (T, error) {
	var t T
	b, err := json.Marshal(params)
	if err != nil {
		return t, err
	}
	return t, json.Unmarshal(b, &t)
}
