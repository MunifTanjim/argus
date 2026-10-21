package pi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
)

// Session file format: JSONL under ~/.pi/agent/sessions/--<cwd>--/<ts>_<id>.jsonl
// (see packages/coding-agent/docs/session-format.md). Each line has a `type`
// field; non-header entries form a tree via id/parentId. Versions 1-3 exist;
// pi migrates on load, so raw files may predate the tree structure.

// sessionHeader is the file's first line: metadata, not part of the tree.
type sessionHeader struct {
	ID   string `json:"id"`
	Cwd  string `json:"cwd"`
	Name string `json:"name,omitempty"`
}

// sessionEntry is one tree entry. Message payloads stay raw here; parse.go
// interprets them.
type sessionEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`

	// compaction / branch_summary
	Summary        string `json:"summary"`
	FirstKeptEntry string `json:"firstKeptEntryId"`
	// context_edit
	TargetID    string          `json:"targetId"`
	Replacement json.RawMessage `json:"replacement"`
	// model_change / thinking_level_change
	Provider    string `json:"provider"`
	ModelID     string `json:"modelId"`
	ThinkingLvl string `json:"thinkingLevel"`
	// usage
	Usage *piUsage `json:"usage"`
	// custom_message
	Content json.RawMessage `json:"content"`
	Display bool            `json:"display"`
	// session_info
	Name string `json:"name"`
}

// replaces reports whether a context_edit replaces its target's content; an
// absent or null replacement omits the target instead.
func (e *sessionEntry) replaces() bool {
	return len(e.Replacement) > 0 && string(e.Replacement) != "null"
}

// piUsage mirrors pi's Usage, less reasoning: output already includes it.
type piUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

func (u *piUsage) tokens() int {
	if u == nil {
		return 0
	}
	return u.Input + u.Output
}

// agentMessage is the envelope of a message entry's payload.
type agentMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Timestamp    int64           `json:"timestamp"`
	Model        string          `json:"model"`
	Usage        *piUsage        `json:"usage"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	IsError      bool            `json:"isError"`
	Command      string          `json:"command"`
	Output       string          `json:"output"`
	ExitCode     *int            `json:"exitCode"`
	Cancelled    bool            `json:"cancelled"`
	Summary      string          `json:"summary"`
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
}

// sessionFile is a parsed session's entries in file (append) order, without
// the header.
type sessionFile struct {
	entries []sessionEntry
	byID    map[string]int // entry id -> index in entries
	offset  int64          // bytes parsed: whole lines only
	head    []byte         // the first line
}

// readSessionFile parses a session .jsonl. A missing file is empty, not an
// error: pi creates the file only after the first user/assistant message.
func readSessionFile(path string) (*sessionFile, error) {
	sf := &sessionFile{}
	return sf, sf.update(path)
}

// update parses the lines appended to path since the last call. pi rewrites a
// file only to migrate it on load, which changes its first line or shortens
// it; either parses it over.
func (sf *sessionFile) update(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < sf.offset || !sf.sameHead(f) {
		*sf = sessionFile{}
	}
	if sf.byID == nil {
		sf.byID = map[string]int{}
	}
	if _, err := f.Seek(sf.offset, io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil // a line without its newline is still being written
		}
		if err != nil {
			return err
		}
		sf.offset += int64(len(line))
		if sf.head == nil {
			sf.head = line
		}
		var e sessionEntry
		if json.Unmarshal(line, &e) != nil || e.Type == "session" {
			continue
		}
		if e.ID != "" {
			sf.byID[e.ID] = len(sf.entries)
		}
		sf.entries = append(sf.entries, e)
	}
}

func (sf *sessionFile) sameHead(f *os.File) bool {
	if sf.head == nil {
		return true
	}
	buf := make([]byte, len(sf.head))
	_, err := f.ReadAt(buf, 0)
	return err == nil && bytes.Equal(buf, sf.head)
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
	slices.Reverse(chain)
	// A chain of one unlinked entry means no tree linkage at all: use file order.
	if len(chain) == 1 && chain[0].ParentID == nil && len(sf.entries) > 1 {
		chain = chain[:0]
		for i := range sf.entries {
			chain = append(chain, &sf.entries[i])
		}
	}
	edits := latestEdits(chain)
	var kept []*sessionEntry
	for _, e := range chain {
		if ed, ok := edits[e.ID]; ok && !ed.replaces() {
			continue
		}
		kept = append(kept, e)
	}
	return applyCompaction(kept)
}

// latestEdits maps each context_edit target on the branch to its latest edit.
func latestEdits(branch []*sessionEntry) map[string]*sessionEntry {
	edits := map[string]*sessionEntry{}
	for _, e := range branch {
		if e.Type == "context_edit" && e.TargetID != "" {
			edits[e.TargetID] = e
		}
	}
	return edits
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
		return chain[latestIdx:]
	}
	return chain[keepFrom:]
}
