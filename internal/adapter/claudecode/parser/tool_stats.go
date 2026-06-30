package parser

import (
	"sort"
	"strconv"
	"strings"
)

// ToolStats aggregates per-tool usage. Sorted by CallCount descending when
// returned by AggregateToolStats.
type ToolStats struct {
	Name            string
	CallCount       int
	TotalDurationMs int64
	ErrorCount      int
}

// AggregateToolStats returns per-tool counts. Subagent dispatches count under
// "Task". Duration sums only count results where DurationMs > 0 (inflated
// concurrent-task durations are already zeroed upstream).
func AggregateToolStats(chunks []Chunk) []ToolStats {
	byName := make(map[string]*ToolStats)

	for _, c := range chunks {
		if c.Type != AIChunk {
			continue
		}
		for _, it := range c.Items {
			name := toolStatsName(it)
			if name == "" {
				continue
			}
			s, ok := byName[name]
			if !ok {
				s = &ToolStats{Name: name}
				byName[name] = s
			}
			s.CallCount++
			if it.DurationMs > 0 {
				s.TotalDurationMs += it.DurationMs
			}
			if it.ToolError {
				s.ErrorCount++
			}
		}
	}

	out := make([]ToolStats, 0, len(byName))
	for _, s := range byName {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CallCount != out[j].CallCount {
			return out[i].CallCount > out[j].CallCount
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// toolStatsName returns the stats name for an item, or "" to skip non-tool items.
func toolStatsName(it DisplayItem) string {
	switch it.Type {
	case ItemToolCall:
		return it.ToolName
	case ItemSubagent:
		return "Task"
	default:
		return ""
	}
}

// toolAbbrev maps tool names to single-char display tokens ("47R 12B 3E").
// Collisions are fine; lowercase disambiguates similar tools (Glob/Grep).
var toolAbbrev = map[string]string{
	"Read":         "R",
	"Write":        "W",
	"Edit":         "E",
	"Bash":         "B",
	"Grep":         "G",
	"Glob":         "g",
	"Task":         "T",
	"WebFetch":     "F",
	"WebSearch":    "S",
	"TodoWrite":    "t",
	"NotebookEdit": "N",
	"LSP":          "L",
}

// ToolAbbrev returns the single-char abbreviation for a tool name, falling
// back to the first character (or "" for an empty name).
func ToolAbbrev(name string) string {
	if abbr, ok := toolAbbrev[name]; ok {
		return abbr
	}
	if name == "" {
		return ""
	}
	return string([]rune(name)[0])
}

// TopTools returns the n highest-call-count tools as "47R 12B 3E".
func TopTools(stats []ToolStats, n int) string {
	if n <= 0 || len(stats) == 0 {
		return ""
	}
	if n > len(stats) {
		n = len(stats)
	}
	var sb strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(strconv.Itoa(stats[i].CallCount))
		sb.WriteString(ToolAbbrev(stats[i].Name))
	}
	return sb.String()
}
