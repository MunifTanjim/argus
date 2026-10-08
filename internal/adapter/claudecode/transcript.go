package claudecode

import (
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// This file is the anti-corruption boundary between the vendored parser and
// argus's entry model. If the parser's types change, only this mapping needs
// fixing.

// ReadTranscriptView does not inline subagent traces: subagent entries carry
// agentId + hasTrace, and each level is fetched on drill (see ReadSubagentView).
func ReadTranscriptView(path string) (TranscriptView, error) {
	return readTranscriptView(path, false)
}

func readTranscriptView(path string, finished bool) (TranscriptView, error) {
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
	return TranscriptView{Entries: foldEntries(pchunks, agentRefs, finished)}, nil
}

// ReadSubagentView folds a single subagent file (resolved by agentID under
// rootPath) with its nested children linked, so the result is drillable one more
// level. Children are suppressed when this subagent's spawnDepth reaches the cap.
// ok is false when no subagent file exists for agentID.
func ReadSubagentView(rootPath, agentID string) (TranscriptView, bool, error) {
	return readSubagentView(rootPath, agentID, false)
}

func readSubagentView(rootPath, agentID string, finished bool) (TranscriptView, bool, error) {
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
	return TranscriptView{Entries: foldEntries(pchunks, agentRefs, finished)}, true, nil
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

// ReadStreamingView folds a transcript for live streaming: subagent entries carry
// AgentID + HasTrace but their Trace is NOT inlined (fetched via a separate
// subscription). Linking uses the parent scan plus meta.json sidecars (so
// still-running subagents link).
func ReadStreamingView(path string) ([]Entry, error) {
	pchunks, err := parser.ReadSession(path)
	if err != nil {
		return nil, err
	}
	refs := parser.AgentIDsByToolID(path)
	addMetaRefs(refs, path, nil)
	return foldEntries(pchunks, refs, false), nil
}

// FindToolDetail returns the full input/result for a tool entry by tool_use id.
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
	d, ok := transcript.FindToolDetail(foldEntries(pchunks, nil, false), toolID)
	return d, ok, nil
}

// foldEntries follows each finished AI turn with a turn_end entry. The last
// turn gets one only once it is no longer ongoing, or when finished (history
// reads: the session is over even if its last turn never completed).
func foldEntries(pchunks []parser.Chunk, agentRefs map[string]string, finished bool) []Entry {
	var out []Entry
	lastDone := finished || !parser.IsOngoing(pchunks)
	interrupted := parser.LastTurnInterrupted(pchunks)
	for i, pc := range pchunks {
		id := strconv.Itoa(i) // parser exposes no UUID; index is stable for append-only reads
		ts := formatTS(pc.Timestamp)
		switch pc.Type {
		case parser.UserChunk:
			if strings.TrimSpace(pc.UserText) != "" {
				out = append(out, Entry{ID: id, Kind: EntryUser, Timestamp: ts, Text: pc.UserText})
			}
			out = append(out, foldItems(pc.Items, agentRefs, id)...)
		case parser.AIChunk:
			items := foldItems(pc.Items, agentRefs, id)
			out = append(out, items...)
			last := i == len(pchunks)-1
			if !last || lastDone {
				out = append(out, turnEnd(pc, items, id, pc.Interrupted || (last && interrupted)))
			}
		case parser.CompactChunk:
			out = append(out, Entry{ID: id, Kind: EntryCompact, Timestamp: ts, Summary: pc.Output})
		case parser.ShellChunk:
			out = append(out, Entry{ID: id, Kind: EntryShell, Timestamp: ts,
				Text: pc.ShellCommand, Detail: pc.Output, IsError: pc.IsError})
		default: // SystemChunk
			out = append(out, Entry{ID: id, Kind: EntrySystem, Timestamp: ts,
				Label: pc.SystemLabel, Detail: pc.Output, IsError: pc.IsError})
		}
	}
	return out
}

func turnEnd(pc parser.Chunk, items []Entry, id string, interrupted bool) Entry {
	e := Entry{
		ID:          id + ".end",
		Kind:        EntryTurnEnd,
		ModelName:   modelDisplayName(pc.Model),
		ModelColor:  modelColorHex(pc.Model),
		StopReason:  pc.StopReason,
		DurationMs:  pc.DurationMs,
		Thinking:    pc.ThinkingCount,
		Usage:       transformUsage(pc.Usage),
		Interrupted: interrupted,
	}
	if !pc.Timestamp.IsZero() {
		e.Timestamp = formatTS(pc.Timestamp.Add(time.Duration(pc.DurationMs) * time.Millisecond))
	}
	for _, it := range items {
		if it.IsToolCall() {
			e.ToolCount++
		}
	}
	if d := parser.ComputeContextDelta(pc.Cycles); d != nil {
		e.HasContext = true
		e.ContextFirstPct = d.FirstUsagePct
		e.ContextPct = d.LastUsagePct
		e.ContextDeltaTokens = d.DeltaTokens
	}
	return e
}

func foldItems(pitems []parser.DisplayItem, agentRefs map[string]string, chunkID string) []Entry {
	var out []Entry
	for j, pit := range pitems {
		if e, ok := foldItem(pit, agentRefs, chunkID+"."+strconv.Itoa(j)); ok {
			out = append(out, e)
		}
	}
	return out
}

func foldItem(pit parser.DisplayItem, agentRefs map[string]string, id string) (Entry, bool) {
	e := Entry{ID: id}
	switch pit.Type {
	case parser.ItemThinking:
		e.Kind = EntryThinking
		e.Text = pit.Text
	case parser.ItemOutput:
		if strings.TrimSpace(pit.Text) == "" {
			return Entry{}, false
		}
		e.Kind = EntryText
		e.Text = pit.Text
	case parser.ItemQueuedPrompt:
		e.Kind = EntryUser
		e.Text = pit.Text
		e.Timestamp = formatTS(pit.Timestamp)
	case parser.ItemTeammateMessage:
		// A teammate is modeled as a subagent variant: identity on the Subagent,
		// message body on the entry Text.
		e.Kind = EntrySubagent
		e.Text = pit.Text
		e.Subagents = []Subagent{{
			Name:       pit.TeammateID,
			Color:      pit.TeammateColor,
			Idle:       pit.TeammateIdle,
			IsTeammate: true,
		}}
	case parser.ItemToolCall:
		if pit.ToolName == "Skill" {
			e.Kind = EntrySkill
		} else {
			e.Kind = EntryTool
		}
		fillTool(&e, pit)
	case parser.ItemSubagent:
		e.Kind = EntrySubagent
		fillTool(&e, pit)
		sub := Subagent{
			ID:   agentRefs[pit.ToolID],
			Name: pit.TeamMemberName, // spawn name from Task/Agent "name" input (e.g. "codex-comments")
			Type: pit.SubagentType,
			Desc: pit.SubagentDesc,
		}
		sub.HasTrace = sub.ID != ""
		e.Subagents = []Subagent{sub}
	default: // ItemMemoryLoad and any future kinds: not surfaced
		return Entry{}, false
	}
	return e, true
}

func fillTool(e *Entry, pit parser.DisplayItem) {
	e.ToolName = pit.ToolName
	e.ToolID = pit.ToolID
	e.ToolInput = string(pit.ToolInput)
	e.InputPreview = pit.ToolSummary
	e.Result = pit.ToolResult
	e.ResultIsError = pit.ToolError
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
