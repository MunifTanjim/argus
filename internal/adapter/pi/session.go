package pi

import (
	"bufio"
	"encoding/json"
	"os"
)

// Session file format: JSONL under ~/.pi/agent/sessions/--<cwd>--/<ts>_<id>.jsonl
// (see packages/coding-agent/docs/session-format.md). Each line has a `type`
// field; non-header entries form a tree via id/parentId. Versions 1-3 exist;
// pi migrates on load, so raw files may predate the tree structure.

// sessionHeader is the file's first line: metadata, not part of the tree.
type sessionHeader struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`
	Name          string `json:"name,omitempty"`
}

// sessionEntry is one tree entry. Message payloads stay raw here; parse.go
// interprets them.
type sessionEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`

	// compaction
	Summary        string          `json:"summary"`
	FirstKeptEntry string          `json:"firstKeptEntryId"`
	TokensBefore   int             `json:"tokensBefore"`
	SystemMessage  json.RawMessage `json:"systemMessage"`
	// branch_summary
	FromID string `json:"fromId"`
	// context_edit
	TargetID    string          `json:"targetId"`
	Replacement json.RawMessage `json:"replacement"`
	HasRepl     bool            // replacement key present (null counts as omit)
	// model_change / thinking_level_change
	Provider    string `json:"provider"`
	ModelID     string `json:"modelId"`
	ThinkingLvl string `json:"thinkingLevel"`
	// usage
	Kind  string   `json:"kind"`
	Usage *piUsage `json:"usage"`
	// custom / custom_message
	CustomType string          `json:"customType"`
	Content    json.RawMessage `json:"content"`
	Display    bool            `json:"display"`
	Data       json.RawMessage `json:"data"`
	// label / session_info
	Label string `json:"label"`
	Name  string `json:"name"`
}

// piUsage mirrors pi's Usage: reasoning is already included in output.
type piUsage struct {
	Input      int    `json:"input"`
	Output     int    `json:"output"`
	CacheRead  int    `json:"cacheRead"`
	CacheWrite int    `json:"cacheWrite"`
	Reasoning  int    `json:"reasoning"`
	Total      int    `json:"totalTokens"`
	Cost       piCost `json:"cost"`
}

type piCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
	Total  float64 `json:"total"`
}

// agentMessage is the envelope of a message entry's payload.
type agentMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Timestamp    int64           `json:"timestamp"`
	API          string          `json:"api"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	Usage        *piUsage        `json:"usage"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	Details      json.RawMessage `json:"details"`
	IsError      bool            `json:"isError"`
	Command      string          `json:"command"`
	Output       string          `json:"output"`
	ExitCode     *int            `json:"exitCode"`
	Cancelled    bool            `json:"cancelled"`
	Truncated    bool            `json:"truncated"`
	Summary      string          `json:"summary"`
	TokensBefore int             `json:"tokensBefore"`
	FromID       string          `json:"fromId"`
	CustomType   string          `json:"customType"`
	Display      bool            `json:"display"`
}

// contentBlock is one element of an assistant/user content array.
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Redacted  bool            `json:"redacted"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Namespace string          `json:"namespace"`
	Arguments json.RawMessage `json:"arguments"`
	Data      string          `json:"data"`
	MimeType  string          `json:"mimeType"`
}

// sessionFile is a parsed session: header plus entries in file (append) order.
type sessionFile struct {
	header  sessionHeader
	entries []sessionEntry
	byID    map[string]int // entry id -> index in entries
}

// readSessionFile parses a session .jsonl. A missing file is empty, not an
// error: pi creates the file only after the first user/assistant message.
func readSessionFile(path string) (*sessionFile, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &sessionFile{byID: map[string]int{}}, nil
		}
		return nil, err
	}
	defer f.Close()
	sf := &sessionFile{byID: map[string]int{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		if first && probe.Type == "session" {
			json.Unmarshal(line, &sf.header)
			first = false
			continue
		}
		first = false
		var e sessionEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		// context_edit: distinguish absent replacement from explicit null.
		var raw map[string]json.RawMessage
		if json.Unmarshal(line, &raw) == nil {
			if r, ok := raw["replacement"]; ok {
				e.HasRepl = true
				e.Replacement = r
			}
		}
		if e.ID != "" {
			sf.byID[e.ID] = len(sf.entries)
		}
		sf.entries = append(sf.entries, e)
	}
	return sf, sc.Err()
}

// activeBranch walks from the last entry up via parentId to the root,
// returning the active path in conversation order. Entries without linkage
// (v1 files, orphans) degrade to file order. Compaction truncates the head:
// entries before the latest compaction's firstKeptEntryId are summarized away.
// Context edits then apply: null omits the target, non-null replaces content.
func (sf *sessionFile) activeBranch() []*sessionEntry {
	if len(sf.entries) == 0 {
		return nil
	}
	// Chain walk from the last entry.
	var chain []*sessionEntry
	seen := map[string]bool{}
	idx := len(sf.entries) - 1
	for idx >= 0 {
		e := &sf.entries[idx]
		if e.ID != "" {
			if seen[e.ID] {
				break
			}
			seen[e.ID] = true
		}
		chain = append(chain, e)
		if e.ParentID == nil {
			break
		}
		next, ok := sf.byID[*e.ParentID]
		if !ok {
			break
		}
		idx = next
	}
	if len(chain) == 0 {
		return nil
	}
	// Reverse to conversation order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	// A chain of one unlinked entry means no tree linkage at all: use file order.
	if len(chain) == 1 && chain[0].ParentID == nil && len(sf.entries) > 1 {
		chain = chain[:0]
		for i := range sf.entries {
			chain = append(chain, &sf.entries[i])
		}
	}
	// Latest compaction truncates everything before firstKeptEntryId.
	// Context edits: latest edit per target wins on this branch.
	edits := map[string]*sessionEntry{}
	for _, e := range chain {
		if e.Type == "context_edit" && e.TargetID != "" {
			edits[e.TargetID] = e
		}
	}
	var nochain []*sessionEntry
	for _, e := range chain {
		if ed, ok := edits[e.ID]; ok && e.ID != "" {
			if !ed.HasRepl || string(ed.Replacement) == "null" {
				continue // omitted from model context
			}
			// Replacement handled at fold time via editFor().
		}
		nochain = append(nochain, e)
	}
	return applyCompaction(nochain)
}

// applyCompaction drops pre-summary entries covered by the latest compaction
// entry on the branch, keeping the compaction entry itself plus everything
// from firstKeptEntryId onward.
func applyCompaction(chain []*sessionEntry) []*sessionEntry {
	latestIdx := -1
	for i, e := range chain {
		if e.Type == "compaction" {
			latestIdx = i
		}
	}
	if latestIdx < 0 {
		return chain
	}
	c := chain[latestIdx]
	if c.FirstKeptEntry == "" {
		return chain
	}
	keepFrom := -1
	for i := latestIdx - 1; i >= 0; i-- {
		if chain[i].ID == c.FirstKeptEntry {
			keepFrom = i
			break
		}
	}
	if keepFrom < 0 {
		// Retain-none (or unknown id): drop everything before the compaction.
		out := make([]*sessionEntry, 0, len(chain)-latestIdx)
		out = append(out, chain[latestIdx:]...)
		return out
	}
	out := make([]*sessionEntry, 0, len(chain))
	out = append(out, chain[keepFrom:]...)
	return out
}

// editFor returns the replacement content for a message entry targeted by a
// context_edit on this branch, or nil when unedited.
func editFor(branch []*sessionEntry, targetID string) *sessionEntry {
	var last *sessionEntry
	for _, e := range branch {
		if e.Type == "context_edit" && e.TargetID == targetID {
			last = e
		}
	}
	return last
}
