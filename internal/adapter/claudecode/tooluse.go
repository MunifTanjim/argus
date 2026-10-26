package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
)

const (
	// mcpToolPrefix starts the name Claude gives every argus mod MCP tool.
	mcpToolPrefix = "mcp__plugin_argus_argus__"
	// Outlasts a permission prompt left open for a while; entries are single-use.
	toolUseTTL      = 24 * time.Hour
	toolUseSweepGap = time.Minute
	toolUseMeta     = "claudecode/toolUseId"
)

type toolUseEntry struct {
	sessionID string
	at        time.Time
}

// toolUseMap links a Claude tool use id to the session whose PreToolUse hook
// announced it.
type toolUseMap struct {
	mu      sync.Mutex
	entries map[string]toolUseEntry
	sweptAt time.Time
}

func (m *toolUseMap) put(toolUseID, sessionID string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = map[string]toolUseEntry{}
	}
	if now.Sub(m.sweptAt) > toolUseSweepGap {
		m.sweptAt = now
		for id, e := range m.entries {
			if now.Sub(e.at) > toolUseTTL {
				delete(m.entries, id)
			}
		}
	}
	m.entries[toolUseID] = toolUseEntry{sessionID: sessionID, at: now}
}

func (m *toolUseMap) take(toolUseID string, now time.Time) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[toolUseID]
	if !ok {
		return "", false
	}
	delete(m.entries, toolUseID)
	if now.Sub(e.at) > toolUseTTL {
		return "", false
	}
	return e.sessionID, true
}

func (m *toolUseMap) record(p hookPayload) {
	if p.SessionID == "" || p.ToolUseID == "" || !strings.HasPrefix(p.ToolName, mcpToolPrefix) {
		return
	}
	m.put(p.ToolUseID, p.SessionID, time.Now())
}

// MCPSessionID answers from the tool use id Claude puts in _meta, which the
// call's PreToolUse hook announced. Each id resolves once.
func (a ccAdapter) MCPSessionID(meta map[string]json.RawMessage) (string, error) {
	toolUseID, err := adapter.MetaSessionID(meta, toolUseMeta)
	if err != nil {
		return "", err
	}
	id, ok := a.toolUses.take(toolUseID, time.Now())
	if !ok {
		return "", fmt.Errorf("%w: no PreToolUse hook announced tool use %s", api.ErrNoCaller, toolUseID)
	}
	return id, nil
}
