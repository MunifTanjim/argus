package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SubagentProcess holds a parsed subagent and its computed metadata.
// Discovery fills timing/usage/Model; LinkSubagents fills Description,
// SubagentType, and ParentTaskID.
type SubagentProcess struct {
	ID            string    // agentId from filename (agent-{id}.jsonl)
	FilePath      string    // full path to subagent JSONL file
	FileModTime   time.Time // last modification time of the JSONL file
	Chunks        []Chunk   // parsed via ReadSession pipeline
	StartTime     time.Time // first message timestamp
	EndTime       time.Time // last message timestamp
	DurationMs    int64
	Usage         Usage  // aggregated from all AI chunks
	Model         string // model from first AI chunk (e.g. "claude-opus-4-6")
	Description   string
	SubagentType  string
	ParentTaskID  string // tool_use_id of spawning Task call
	TeamSummary   string // summary attr from first <teammate-message> (team agents only)
	TeammateColor string // color attr from first <teammate-message> (team agents only)
}

// DiscoverSubagents parses subagent files under {sessionDir}/{sessionUUID}/subagents/,
// skipping empty files, warmup agents, and compact agents. Result is sorted by StartTime.
func DiscoverSubagents(sessionPath string) ([]SubagentProcess, error) {
	dir := filepath.Dir(sessionPath)
	base := strings.TrimSuffix(filepath.Base(sessionPath), ".jsonl")
	subagentsDir := filepath.Join(dir, base, "subagents")

	entries, err := os.ReadDir(subagentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var procs []SubagentProcess

	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		agentID := strings.TrimPrefix(name, "agent-")
		agentID = strings.TrimSuffix(agentID, ".jsonl")

		// Compact agents are context-compaction artifacts, not real subagents.
		if strings.HasPrefix(agentID, "acompact") {
			continue
		}

		filePath := filepath.Join(subagentsDir, name)

		info, err := de.Info()
		if err != nil || info.Size() == 0 {
			continue
		}

		if isWarmupAgent(filePath) {
			continue
		}

		chunks, teamSummary, teamColor, err := readSubagentSession(filePath)
		if err != nil || len(chunks) == 0 {
			continue
		}

		startTime, endTime, durationMs := chunkTiming(chunks)
		usage := aggregateUsage(chunks)

		procs = append(procs, SubagentProcess{
			ID:            agentID,
			FilePath:      filePath,
			FileModTime:   info.ModTime(),
			Chunks:        chunks,
			StartTime:     startTime,
			EndTime:       endTime,
			DurationMs:    durationMs,
			Usage:         usage,
			Model:         extractModel(chunks),
			TeamSummary:   teamSummary,
			TeammateColor: teamColor,
		})
	}

	sort.Slice(procs, func(i, j int) bool {
		return procs[i].StartTime.Before(procs[j].StartTime)
	})

	return procs, nil
}

// isWarmupAgent reports whether the first user message content is exactly
// "Warmup". Matches claude-devtools behavior for marking warmup agents.
func isWarmupAgent(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	lr := newLineReader(f)
	for {
		line, ok := lr.next()
		if !ok {
			break
		}

		var partial struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &partial); err != nil {
			continue
		}
		if partial.Type != "user" {
			continue
		}

		// content may be a JSON string or array; only string "Warmup" counts.
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(partial.Message, &msg); err != nil {
			return false
		}

		var content string
		if err := json.Unmarshal(msg.Content, &content); err != nil {
			return false
		}
		return content == "Warmup"
	}
	return false
}

// chunkTiming computes start/end timestamps and duration from a chunk slice.
func chunkTiming(chunks []Chunk) (start, end time.Time, durationMs int64) {
	for _, c := range chunks {
		if c.Timestamp.IsZero() {
			continue
		}
		if start.IsZero() || c.Timestamp.Before(start) {
			start = c.Timestamp
		}
		if end.IsZero() || c.Timestamp.After(end) {
			end = c.Timestamp
		}
	}
	if !start.IsZero() && !end.IsZero() {
		durationMs = end.Sub(start).Milliseconds()
	}
	return
}

// readSubagentSession reads a subagent JSONL file, returning chunks plus team
// summary/color (extracted before Classify strips XML attrs). Unlike ReadSession
// it ignores isSidechain: subagent entries are all flagged isSidechain=true but
// represent the subagent's own main conversation.
func readSubagentSession(path string) ([]Chunk, string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", "", err
	}
	defer f.Close()

	lr := newLineReader(f)

	var msgs []ClassifiedMsg
	var teamSummary, teamColor string
	extractedTeamMeta := false
	for {
		line, ok := lr.next()
		if !ok {
			break
		}
		entry, ok := ParseEntry([]byte(line))
		if !ok {
			continue
		}

		// Pull team summary/color from the first user entry's <teammate-message>
		// tag before Classify strips the XML attributes.
		if !extractedTeamMeta && entry.Type == "user" {
			var contentStr string
			if json.Unmarshal(entry.Message.Content, &contentStr) == nil {
				if m := teammateSummaryRe.FindStringSubmatch(contentStr); len(m) > 1 {
					teamSummary = m[1]
				}
				if m := teammateColorRe.FindStringSubmatch(contentStr); len(m) > 1 {
					teamColor = m[1]
				}
				extractedTeamMeta = true
			}
		}

		// Clear sidechain flag so Classify doesn't filter these out.
		entry.IsSidechain = false
		msg, ok := Classify(entry)
		if !ok {
			continue
		}
		msgs = append(msgs, msg)
	}
	if err := lr.Err(); err != nil {
		return nil, "", "", err
	}

	return BuildChunks(msgs), teamSummary, teamColor, nil
}

// extractModel returns the model string from the first AI chunk, or "".
func extractModel(chunks []Chunk) string {
	for _, c := range chunks {
		if c.Type == AIChunk && c.Model != "" {
			return c.Model
		}
	}
	return ""
}

// aggregateUsage returns the last non-zero AI chunk usage snapshot — the
// subagent's context state at completion.
func aggregateUsage(chunks []Chunk) Usage {
	for i := len(chunks) - 1; i >= 0; i-- {
		if chunks[i].Type == AIChunk && chunks[i].Usage.TotalTokens() > 0 {
			return chunks[i].Usage
		}
	}
	return Usage{}
}
