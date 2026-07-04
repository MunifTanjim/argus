package claudecode

import (
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

// This file is the anti-corruption boundary between the vendored parser and
// argus's chunk model: the parser does all JSONL work, we map its output into the
// stable claudecode types. If the parser's types change, only this mapping needs
// fixing.

// ReadTranscriptView reads a transcript via the parser and maps it into argus's
// chunk model. Subagent items carry agentId + hasTrace; traces are NOT inlined,
// each level is fetched on drill (see ReadSubagentView).
func ReadTranscriptView(path string) (TranscriptView, error) {
	pchunks, err := parser.ReadSession(path)
	if err != nil {
		return TranscriptView{}, err
	}
	agentRefs := map[string]string{}
	if procs, derr := parser.DiscoverSubagents(path); derr == nil && len(procs) > 0 {
		parser.LinkSubagents(procs, pchunks, path)
		for _, p := range procs {
			if p.ParentTaskID != "" {
				agentRefs[p.ParentTaskID] = p.ID
			}
		}
	}
	return TranscriptView{Chunks: foldChunks(pchunks, agentRefs, nil)}, nil
}

// ReadSubagentView folds a single subagent file (resolved by agentID under
// rootPath) with its nested children linked, so the result is drillable one more
// level. Children are suppressed when this subagent's spawnDepth reaches the cap.
// ok is false when no subagent file exists for agentID.
func ReadSubagentView(rootPath, agentID string) (TranscriptView, bool, error) {
	sub, ok := parser.SubagentFilePath(rootPath, agentID)
	if !ok {
		return TranscriptView{}, false, nil
	}
	pchunks, err := parser.ReadSubagentSession(sub)
	if err != nil {
		return TranscriptView{}, false, err
	}
	var agentRefs map[string]string
	if parser.SpawnDepth(rootPath, agentID) < parser.MaxSubagentDepth {
		agentRefs = parser.ChildAgentRefs(sub, rootPath)
	}
	return TranscriptView{Chunks: foldChunks(pchunks, agentRefs, nil)}, true, nil
}

// addMetaRefs merges meta.json-derived refs (which link still-running subagents)
// into agentRefs; existing link-based refs win. cache may be nil (no memoization).
func addMetaRefs(agentRefs map[string]string, rootPath string, cache map[string]string) {
	for tid, aid := range parser.MetaAgentRefs(rootPath, cache) {
		if _, ok := agentRefs[tid]; !ok {
			agentRefs[tid] = aid
		}
	}
}

// ReadStreamingView folds a transcript for live streaming: subagent items carry
// AgentID + HasTrace but their Trace is NOT inlined (fetched via a separate
// subscription). Linking uses the parent scan plus meta.json sidecars (so
// still-running subagents link).
func ReadStreamingView(path string) ([]Chunk, error) {
	pchunks, err := parser.ReadSession(path)
	if err != nil {
		return nil, err
	}
	refs := parser.AgentIDsByToolID(path)
	addMetaRefs(refs, path, nil)
	return foldChunks(pchunks, refs, nil), nil
}

// FindToolDetail returns the full input/result for a tool item by tool_use id.
// agentID selects a subagent file; empty searches the session transcript.
func FindToolDetail(path, agentID, toolID string) (ToolDetail, bool, error) {
	read := parser.ReadSession
	if agentID != "" {
		sub, ok := parser.SubagentFilePath(path, agentID)
		if !ok {
			return ToolDetail{}, false, nil
		}
		path = sub
		// Subagent files mark every entry isSidechain=true; ReadSubagentSession
		// clears the flag so Classify keeps them (ReadSession would drop them all).
		read = parser.ReadSubagentSession
	}
	pchunks, err := read(path)
	if err != nil {
		return ToolDetail{}, false, err
	}
	for _, c := range foldChunks(pchunks, nil, nil) {
		for _, it := range c.Items {
			if (it.Kind == ItemTool || it.Kind == ItemSubagent) && it.ToolID == toolID {
				return ToolDetail{ToolInput: it.ToolInput, Result: it.Result, ResultIsError: it.ResultIsError}, true, nil
			}
		}
	}
	return ToolDetail{}, false, nil
}

// foldChunks maps parser chunks to display chunks. agentRefs links subagent
// items to their agent ids; traces (optional) inlines each subagent's trace.
func foldChunks(pchunks []parser.Chunk, agentRefs map[string]string, traces map[string][]Chunk) []Chunk {
	out := make([]Chunk, 0, len(pchunks))
	for i, pc := range pchunks {
		out = append(out, foldChunk(pc, agentRefs, traces, i))
	}
	return out
}

func foldChunk(pc parser.Chunk, agentRefs map[string]string, traces map[string][]Chunk, idx int) Chunk {
	c := Chunk{
		ID:        strconv.Itoa(idx), // parser exposes no UUID; index is stable for append-only reads
		Timestamp: formatTS(pc.Timestamp),
	}
	switch pc.Type {
	case parser.UserChunk:
		c.Kind = ChunkUser
		c.Text = pc.UserText
	case parser.AIChunk:
		c.Kind = ChunkAI
		c.Model = pc.Model
		c.StopReason = pc.StopReason
		c.DurationMs = pc.DurationMs
		c.Thinking = pc.ThinkingCount
		c.Usage = transformUsage(pc.Usage)
		c.Items = foldItems(pc.Items, agentRefs, traces)
		for _, it := range c.Items {
			if it.Kind == ItemTool || it.Kind == ItemSubagent {
				c.ToolCount++
			}
		}
		if d := parser.ComputeContextDelta(pc.Cycles); d != nil {
			c.HasContext = true
			c.ContextFirstPct = d.FirstUsagePct
			c.ContextPct = d.LastUsagePct
			c.ContextDeltaTokens = d.DeltaTokens
		}
	case parser.CompactChunk:
		c.Kind = ChunkCompact
		c.Summary = pc.Output
	case parser.ShellChunk:
		c.Kind = ChunkShell
		c.Text = pc.ShellCommand
		c.Detail = pc.Output
		c.IsError = pc.IsError
	case parser.SkillChunk:
		c.Kind = ChunkSkill
		c.Text = pc.UserText     // skill identifier
		c.Label = pc.SystemLabel // source base directory
		c.Detail = pc.Output     // skill file body
	default: // SystemChunk
		c.Kind = ChunkSystem
		c.Label = pc.SystemLabel // preview after the timestamp (e.g. "Recap"); "" for none
		c.Detail = pc.Output
		c.IsError = pc.IsError
	}
	return c
}

func foldItems(pitems []parser.DisplayItem, agentRefs map[string]string, traces map[string][]Chunk) []Item {
	var out []Item
	for j, pit := range pitems {
		it, ok := foldItem(pit, agentRefs, traces, j)
		if ok {
			out = append(out, it)
		}
	}
	return out
}

// foldItem maps a parser DisplayItem to an argus Item. ok is false for item
// kinds argus doesn't surface (e.g. memory-load pills).
func foldItem(pit parser.DisplayItem, agentRefs map[string]string, traces map[string][]Chunk, idx int) (Item, bool) {
	it := Item{ID: strconv.Itoa(idx)}
	switch pit.Type {
	case parser.ItemThinking:
		it.Kind = ItemThinking
		it.Text = pit.Text
	case parser.ItemOutput, parser.ItemTeammateMessage:
		it.Kind = ItemText
		it.Text = pit.Text
	case parser.ItemToolCall:
		it.Kind = ItemTool
		fillTool(&it, pit)
	case parser.ItemSubagent:
		it.Kind = ItemSubagent
		fillTool(&it, pit)
		sub := Subagent{
			ID:   agentRefs[pit.ToolID],
			Type: pit.SubagentType,
			Desc: pit.SubagentDesc,
		}
		if traces != nil {
			sub.Trace = traces[pit.ToolID]
		}
		sub.HasTrace = len(sub.Trace) > 0 || sub.ID != ""
		it.Subagents = []Subagent{sub}
	default: // ItemMemoryLoad and any future kinds: not surfaced
		return Item{}, false
	}
	return it, true
}

func fillTool(it *Item, pit parser.DisplayItem) {
	it.ToolName = pit.ToolName
	it.ToolID = pit.ToolID
	it.ToolInput = string(pit.ToolInput)
	it.InputPreview = pit.ToolSummary
	it.Result = pit.ToolResult
	it.ResultIsError = pit.ToolError
}

func transformUsage(u parser.Usage) Usage {
	return Usage{
		Input:         u.InputTokens,
		Output:        u.OutputTokens,
		CacheRead:     u.CacheReadTokens,
		CacheCreation: u.CacheCreationTokens,
	}
}

func formatTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func firstLineOf(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
