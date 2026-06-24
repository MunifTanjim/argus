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
// Discovery fills ID, FilePath, Chunks, timing, usage, and Model.
// LinkSubagents fills Description, SubagentType, and ParentTaskID.
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

// DiscoverSubagents finds and parses subagent files for a session.
//
// Takes the full path to a session JSONL file (e.g.
// ~/.claude/projects/{projectId}/{sessionUUID}.jsonl) and derives the
// subagents directory: {sessionDir}/{sessionUUID}/subagents/
//
// Filters out:
//   - Empty files
//   - Warmup agents (first user message content is exactly "Warmup")
//   - Compact agents (agentId starts with "acompact")
//
// Returns parsed SubagentProcesses sorted by StartTime.
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

		// Filter compact agents (context compaction artifacts).
		if strings.HasPrefix(agentID, "acompact") {
			continue
		}

		filePath := filepath.Join(subagentsDir, name)

		// Filter empty files.
		info, err := de.Info()
		if err != nil || info.Size() == 0 {
			continue
		}

		// Filter warmup agents by checking first user message content.
		if isWarmupAgent(filePath) {
			continue
		}

		// Parse through the pipeline with sidechain filtering disabled.
		// Subagent entries all have isSidechain=true (they run in the
		// parent's sidechain context), but within the subagent file
		// they're the main conversation.
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

// isWarmupAgent reads just enough of a subagent file to check if the first
// user message content is exactly "Warmup". Matches claude-devtools behavior:
// the first entry with type=user and string content "Warmup" marks a warmup agent.
func isWarmupAgent(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Read just enough to find the first user entry. Subagent files are
	// small-ish and the first entry is almost always the user message,
	// so scanning a few lines is fine.
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

		// Extract message.content -- could be a JSON string or array.
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(partial.Message, &msg); err != nil {
			return false
		}

		// Only string content "Warmup" counts.
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

// readSubagentSession reads a subagent JSONL file and returns chunks plus
// team metadata (summary and color). Both are extracted from the raw entry
// content before Classify strips the XML tag attributes.
//
// Unlike ReadSession, it ignores the isSidechain flag since all entries
// in subagent files are marked isSidechain=true but represent the
// subagent's own main conversation.
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

		// Extract team summary and color from the first user entry's
		// <teammate-message> tag before Classify strips the XML attributes.
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

// aggregateUsage returns the last AI chunk's usage snapshot. Each chunk already
// holds the last assistant message's context-window snapshot, so the final
// chunk's snapshot represents the subagent's context state at completion.
func aggregateUsage(chunks []Chunk) Usage {
	for i := len(chunks) - 1; i >= 0; i-- {
		if chunks[i].Type == AIChunk && chunks[i].Usage.TotalTokens() > 0 {
			return chunks[i].Usage
		}
	}
	return Usage{}
}
