package tui

import (
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// Display rows sit between entries and layout: a run of thinking/tool entries
// folds into a one-line summary, or unfolds between a head and a foot control
// row. The mobile app groups the same way (app/lib/ui/transcript_feed.dart).

type rowKind int

const (
	entryRow   rowKind = iota
	runSummary         // a collapsed run
	runHead            // the top control row of an expanded run
	runFoot            // the bottom control row of an expanded run
)

// runInfo describes a run: its key (the first entry's id), its counts, and its
// [first,last] entry index range.
type runInfo struct {
	key             string
	thinking, tools int
	first, last     int
}

type displayRow struct {
	kind  rowKind
	entry int // index into the entries, for entry rows
	run   runInfo
}

func (r displayRow) isControl() bool { return r.kind != entryRow }

// rowRef is a row's identity across refreshes: the entry id for an entry row,
// the run key for a control row.
type rowRef struct {
	kind rowKind
	id   string
}

func rowID(es []transcript.Entry, r displayRow) rowRef {
	if r.kind == entryRow {
		return rowRef{id: es[r.entry].ID}
	}
	return rowRef{kind: r.kind, id: r.run.key}
}

// isSpawn reports a subagent spawn (not an agent-ref op like wait_agent): it
// stands alone so it stays visible and drillable.
func isSpawn(e transcript.Entry) bool {
	return e.Kind == transcript.EntrySubagent && !e.IsTeammate() && !isAgentRefTool(e.ToolName)
}

func inRun(e transcript.Entry) bool {
	return e.Kind == transcript.EntryThinking || (e.IsToolCall() && !isSpawn(e))
}

// buildRows folds each maximal run of thinking/tool entries.
func buildRows(es []transcript.Entry, verbose bool, overrides map[string]bool) []displayRow {
	rows := make([]displayRow, 0, len(es))
	for i := 0; i < len(es); {
		if !inRun(es[i]) {
			rows = append(rows, displayRow{kind: entryRow, entry: i})
			i++
			continue
		}
		run := runInfo{key: es[i].ID, first: i}
		if run.key == "" {
			run.key = "#" + strconv.Itoa(i)
		}
		for ; i < len(es) && inRun(es[i]); i++ {
			if es[i].Kind == transcript.EntryThinking {
				run.thinking++
			} else {
				run.tools++
			}
		}
		run.last = i - 1
		expanded, ok := overrides[run.key]
		if !ok {
			expanded = verbose
		}
		if !expanded {
			rows = append(rows, displayRow{kind: runSummary, run: run})
			continue
		}
		rows = append(rows, displayRow{kind: runHead, run: run})
		for j := run.first; j <= run.last; j++ {
			rows = append(rows, displayRow{kind: entryRow, entry: j, run: run})
		}
		rows = append(rows, displayRow{kind: runFoot, run: run})
	}
	return rows
}

// rowIndexOf finds the row with identity ref. A control row of a run that
// changed state, or an entry now folded into a run, resolves to the run's
// summary or head.
func rowIndexOf(es []transcript.Entry, rows []displayRow, ref rowRef) int {
	if ref.id == "" {
		return -1
	}
	for i, r := range rows {
		if rowID(es, r) == ref {
			return i
		}
	}
	if ref.kind != entryRow {
		return runRowIndex(rows, ref.id)
	}
	for j, e := range es {
		if e.ID != ref.id {
			continue
		}
		for i, r := range rows {
			if (r.kind == runSummary || r.kind == runHead) && r.run.first <= j && j <= r.run.last {
				return i
			}
		}
	}
	return -1
}

// restoreRowCursor resolves a cursor after its rows changed: the last row when
// toLast, else the row with identity ref, else fallback clamped to the rows.
func restoreRowCursor(es []transcript.Entry, rows []displayRow, ref rowRef, fallback int, toLast bool) int {
	if toLast && len(rows) > 0 {
		return len(rows) - 1
	}
	if i := rowIndexOf(es, rows, ref); i >= 0 {
		return i
	}
	return max(0, min(fallback, len(rows)-1))
}

// runRowIndex finds the summary (collapsed) or head (expanded) row of run key.
func runRowIndex(rows []displayRow, key string) int {
	for i, r := range rows {
		if (r.kind == runSummary || r.kind == runHead) && r.run.key == key {
			return i
		}
	}
	return -1
}

// runCounts renders the thinking and tool counts with the turn footer's icons.
func runCounts(thinking, tools int) string {
	var parts []string
	if thinking > 0 {
		parts = append(parts, Icon.Thinking.WithColor(ColorRunThinking)+StyleDim.Render(" "+strconv.Itoa(thinking)))
	}
	if tools > 0 {
		parts = append(parts, Icon.Tool.Ok.WithColor(ColorRunTool)+StyleDim.Render(" "+strconv.Itoa(tools)))
	}
	return strings.Join(parts, StyleDim.Render(" · "))
}

func controlText(r displayRow) string {
	var chev string
	switch r.kind {
	case runSummary:
		chev = Icon.Collapsed.Render()
	case runFoot:
		chev = Icon.Collapse.Render()
	default:
		chev = Icon.Expanded.Render()
	}
	// Two spaces: nerd-font chevrons often draw wider than the one cell they
	// measure, which would swallow a single space.
	return chev + "  " + runCounts(r.run.thinking, r.run.tools)
}

func controlBlock(r displayRow, selected, focused bool, width int) string {
	return gutterBar(selected, focused) + truncateLine(controlText(r), max(width-detailGutter, 10))
}
