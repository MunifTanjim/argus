package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestFilledTool(t *testing.T) {
	m := bareTv()
	m.toolBodies = map[string]toolBodyEntry{
		"loadingTool": {loading: true},
		"doneTool":    {done: true, toolInput: `{"x":1}`, result: "out", resultIsError: true},
	}

	// No ToolID → not addressable → treated as already resolved (renders inline).
	if _, fetched := m.filledTool(transcript.Entry{Kind: transcript.EntryTool, ToolInput: "inline"}); !fetched {
		t.Error("item without ToolID should be reported as fetched")
	}
	// Outstanding fetch → not yet fetched (caller shows a placeholder).
	if _, fetched := m.filledTool(transcript.Entry{Kind: transcript.EntryTool, ToolID: "loadingTool"}); fetched {
		t.Error("loading item should not be reported as fetched")
	}
	// Unknown id → not fetched.
	if _, fetched := m.filledTool(transcript.Entry{Kind: transcript.EntryTool, ToolID: "unknown"}); fetched {
		t.Error("unknown tool should not be reported as fetched")
	}
	// Completed fetch → fields filled from the cache.
	got, fetched := m.filledTool(transcript.Entry{Kind: transcript.EntryTool, ToolID: "doneTool"})
	if !fetched {
		t.Fatal("done item should be reported as fetched")
	}
	if got.ToolInput != `{"x":1}` || got.Result != "out" || !got.ResultIsError {
		t.Errorf("filled item = %+v", got)
	}
}

func detailTestModel(c transcript.Entry) tview {
	mm := withView(testModel(), viewSession)
	m := tvOf(&mm)
	m.transcript.entries = []transcript.Entry{c}
	m.transcript.runs[c.ID] = true // a lone tool or thinking entry is a run
	m.transcript.cursor = rowIndexOf(m.transcript.entries, m.displayRows(), rowRef{id: c.ID})
	m.historyView = histDetail
	m.enterDetail()
	m.put()
	return m
}

// traceFixture wraps entries in an inline subagent trace of type typ, so
// enterDetail opens a navigable frame listing them.
func traceFixture(typ string, items ...transcript.Entry) transcript.Entry {
	return transcript.Entry{ID: "a", Kind: transcript.EntrySubagent, ToolName: "Task",
		Subagents: []transcript.Subagent{{Type: typ, HasTrace: true, Trace: items}}}
}

// maxLineWidth returns the widest visible line width in s (ANSI-aware).
func maxLineWidth(s string) int {
	w := 0
	for _, line := range strings.Split(s, "\n") {
		if lw := lipgloss.Width(line); lw > w {
			w = lw
		}
	}
	return w
}

func TestDetailBodyCentersOnWideTerminal(t *testing.T) {
	m := detailTestModel(traceFixture("Opus 4.8", transcript.Entry{Kind: transcript.EntryText, Text: "hi"}))
	m.c.m.width = 200 // > maxContentWidth (160) → centerBlock adds a left gutter
	m.c.m.height = 40
	m.c.m.viewer = true // centering applies only to the bare full-screen viewer
	out := m.detailBody()
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			t.Errorf("expected left gutter (centered) on wide terminal: %q", line)
			break
		}
	}
}

func TestDetailItemsWrapLongContent(t *testing.T) {
	width := 40
	longCmd := "echo " + strings.Repeat("verylongtokenwithoutspaces", 8)
	longJSON := `{"k":"` + strings.Repeat("x", 200) + `"}`
	m := bareTv()

	cases := map[string]transcript.Entry{
		"bash command": {Kind: transcript.EntryTool, ToolName: "Bash",
			ToolInput: `{"command":"` + longCmd + `"}`},
		"json result": {Kind: transcript.EntryTool, ToolName: "UnknownTool",
			Result: longJSON},
		"edit diff": {Kind: transcript.EntryTool, ToolName: "Edit",
			ToolInput: `{"file_path":"a.go","old_string":"short","new_string":"` + strings.Repeat("z", 200) + `"}`},
	}
	for name, it := range cases {
		// Gutter adds 2 columns, so the wrapped body must stay within width.
		out := m.entryBlock(it, true, false, true, true, width)
		if got := maxLineWidth(out); got > width {
			t.Errorf("%s: line width %d > %d:\n%s", name, got, width, out)
		}
	}
}

func TestCollapsedRowFitsWidth(t *testing.T) {
	m := bareTv()
	it := transcript.Entry{Kind: transcript.EntryTool, ToolName: "Bash",
		InputPreview: strings.Repeat("a long command preview ", 6)}
	out := m.entryBlock(it, false, false, true, false, 40)
	if got := maxLineWidth(out); got > 40 {
		t.Errorf("collapsed row width %d > 40:\n%s", got, out)
	}
	if n := strings.Count(out, "\n") + 1; n != 1 {
		t.Errorf("collapsed row should be a single line, got %d:\n%s", n, out)
	}
}

func TestPermissionPromptWrapsLongCommand(t *testing.T) {
	width := 40
	ix := &session.Interaction{
		Kind:      session.InteractionPermission,
		ToolName:  "Bash",
		ToolInput: `{"command":"` + strings.Repeat("a", 200) + `"}`,
		Message:   strings.Repeat("permission message ", 10),
	}
	m := testModel()
	out := interactionBody(m, "", ix, width)
	if got := maxLineWidth(out); got > width {
		t.Errorf("permission body line width %d > %d:\n%s", got, width, out)
	}
}

func TestDetailScrollHint(t *testing.T) {
	// Build a trace whose render far exceeds a tiny viewport.
	var items []transcript.Entry
	for i := 0; i < 40; i++ {
		items = append(items, transcript.Entry{Kind: transcript.EntryText, Text: "line of output"})
	}
	m := detailTestModel(traceFixture("Explore", items...))
	m.c.m.width, m.c.m.height = 80, 12 // viewport 5 under the frame header

	out := m.detailBody()
	if !strings.Contains(out, "▼") {
		t.Errorf("expected a down-scroll hint when content overflows:\n%s", out)
	}
	// Body must still fit the viewport height.
	if got := strings.Count(out, "\n") + 1; got > m.viewportHeight() {
		t.Errorf("detail body %d lines > viewport %d", got, m.viewportHeight())
	}

	// Scrolled to the bottom, the hint shows lines hidden above (▲).
	m.topFrame().scroll = 9999 // detailBody clamps to the last full page
	if out := m.detailBody(); !strings.Contains(out, "▲") {
		t.Errorf("expected an up-scroll hint when scrolled down:\n%s", out)
	}
}

func TestEnterDrillPopStack(t *testing.T) {
	sub := transcript.Entry{
		ID: "s", Kind: transcript.EntrySubagent,
		Subagents: []transcript.Subagent{{Type: "explorer", HasTrace: true,
			Trace: []transcript.Entry{
				{Kind: transcript.EntryTool, ToolName: "Read"},
				{Kind: transcript.EntryTool, ToolName: "Grep"},
			},
		}},
	}
	m := bareTv()
	m.c.m.verboseTranscript = true // list each tool, not the run's summary
	m.transcript.entries = []transcript.Entry{{ID: "t", Kind: transcript.EntryText, Text: "hi"}, sub}
	m.transcript.cursor = 1
	m.enterDetail()
	if len(m.transcript.detailStack) != 1 || len(m.topFrame().items) != 2 || m.topFrame().label != "explorer" {
		t.Fatalf("trace frame: frames=%d label=%q", len(m.transcript.detailStack), m.topFrame().label)
	}
	if m.topFrame().defaultExpanded {
		t.Error("drilled subagent children should start collapsed")
	}
	// Drill into a leaf (item 0, past the run's head row).
	m.topFrame().cursor = 1
	m.actDetailDrill(tea.KeyPressMsg{})
	if len(m.transcript.detailStack) != 2 || !m.topFrame().focused {
		t.Fatalf("drill: frames=%d focused=%v", len(m.transcript.detailStack), m.topFrame().focused)
	}
	if m.popDetail() {
		t.Error("popping to root should not empty the stack")
	}
	if !m.popDetail() {
		t.Error("popping the root should empty the stack")
	}
}

func TestDetailDrillSkipsNonDetailable(t *testing.T) {
	m := bareTv()
	m.transcript.entries = []transcript.Entry{traceFixture("explorer",
		transcript.Entry{Kind: transcript.EntryTurnEnd},
		transcript.Entry{Kind: transcript.EntryCompact, Summary: "compacted"},
	)}
	m.enterDetail()
	for i := range 2 {
		m.topFrame().cursor = i
		m.actDetailDrill(tea.KeyPressMsg{})
		m.clickItem(i, true)
		if len(m.transcript.detailStack) != 1 {
			t.Fatalf("item %d (%s) drilled: frames=%d", i, m.topFrame().items[0].Kind, len(m.transcript.detailStack))
		}
	}
}

func TestDetailBodyShowsBreadcrumbAndRows(t *testing.T) {
	m := detailTestModel(traceFixture("Opus 4.8",
		transcript.Entry{Kind: transcript.EntryText, Text: "hello"},
		transcript.Entry{Kind: transcript.EntryTool, ToolName: "Bash", ToolInput: `{"command":"ls"}`},
	))
	m.c.m.width, m.c.m.height = 80, 30
	m.c.m.verboseTranscript = true // list the tool, not its run's summary
	if m.topFrame().isExpanded(1) {
		t.Errorf("trace items should start collapsed")
	}
	out := m.detailBody()
	if !strings.Contains(out, "Opus 4.8") {
		t.Errorf("breadcrumb missing root label:\n%s", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "Bash") {
		t.Errorf("frame rows missing:\n%s", out)
	}
	// Drill into a focused item → breadcrumb grows.
	m.topFrame().cursor = 2 // the tool, inside its run
	m.actDetailDrill(tea.KeyPressMsg{})
	if out := m.detailBody(); !strings.Contains(out, "Opus 4.8 › Bash") {
		t.Errorf("drilled breadcrumb missing:\n%s", out)
	}
}

// A non-drillable entry opens a focus frame once; further Enter presses on the
// focus frame must not keep nesting the same entry.
func TestFocusFrameDoesNotRenest(t *testing.T) {
	m := detailTestModel(transcript.Entry{ID: "a", Kind: transcript.EntryTool, ToolName: "Bash", ToolInput: `{"command":"ls"}`})
	m.c.m.width, m.c.m.height = 80, 30

	if len(m.transcript.detailStack) != 1 || !m.topFrame().focused {
		t.Fatalf("drill should focus once: frames=%d focused=%v", len(m.transcript.detailStack), m.topFrame().focused)
	}
	for i := 0; i < 3; i++ {
		m.actDetailDrill(tea.KeyPressMsg{}) // re-pressing Enter must be a no-op on a focus frame
	}
	if len(m.transcript.detailStack) != 1 {
		t.Fatalf("focus frame re-nested: frames=%d", len(m.transcript.detailStack))
	}
}

func TestDrillableUsesHasTrace(t *testing.T) {
	withTrace := transcript.Entry{Kind: transcript.EntrySubagent, Subagents: []transcript.Subagent{{ID: "a1", HasTrace: true}}}
	if !drillable(withTrace) {
		t.Error("subagent with HasTrace should be drillable")
	}
	plain := transcript.Entry{Kind: transcript.EntryTool, ToolName: "Read"}
	if drillable(plain) {
		t.Error("non-subagent should not be drillable")
	}
}

func TestDetailKeyNav(t *testing.T) {
	sub := transcript.Entry{Kind: transcript.EntrySubagent, Subagents: []transcript.Subagent{{Type: "explorer", HasTrace: true,
		Trace: []transcript.Entry{{Kind: transcript.EntryTool, ToolName: "Read"}}}}}
	m := detailTestModel(traceFixture("Opus 4.8", transcript.Entry{Kind: transcript.EntryText, Text: "hi"}, sub))
	m.c.m.width, m.c.m.height = 80, 30

	m.handleDetailKey(tea.KeyPressMsg{Code: 'j'})
	if m.topFrame().cursor != 1 {
		t.Fatalf("cursor=%d want 1", m.topFrame().cursor)
	}
	// Trace items start collapsed, so l expands the selected item.
	before := m.topFrame().isExpanded(1)
	m.handleDetailKey(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if m.topFrame().isExpanded(1) == before {
		t.Error("l should expand the selected item")
	}
	m.handleDetailKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.transcript.detailStack) != 2 || m.topFrame().label != "explorer" {
		t.Fatalf("enter should drill: frames=%d", len(m.transcript.detailStack))
	}
}

func TestDetailSubagentShowsNicknameAndInput(t *testing.T) {
	m := bareTv()
	it := transcript.Entry{
		Kind: transcript.EntrySubagent,
		Subagents: []transcript.Subagent{{
			Type:   "default",
			Name:   "Volta",
			Desc:   "This is the full task message given to the subagent and it is quite long",
			Status: "closed",
		}},
	}
	out := m.entryBlock(it, true, false, true, true, 200)
	if !strings.Contains(out, "Volta") {
		t.Errorf("expected nickname Volta in output, got:\n%s", out)
	}
	if !strings.Contains(out, "default") {
		t.Errorf("expected type default in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Input") {
		t.Errorf("expected Input label in output, got:\n%s", out)
	}
	if !strings.Contains(out, "quite long") {
		t.Errorf("expected full (untruncated) input message, got:\n%s", out)
	}
}

// TestDrillIntoSubagentShowsNicknameAndInput verifies subagent identity and input
// carry into the drilled-in trace frame.
func TestDrillIntoSubagentShowsNicknameAndInput(t *testing.T) {
	sub := transcript.Entry{
		Kind: transcript.EntrySubagent,
		Subagents: []transcript.Subagent{{
			Type: "default", Name: "Volta", Status: "closed",
			Desc:     "the full task message given to the subagent",
			HasTrace: true,
			Trace: []transcript.Entry{
				{Kind: transcript.EntryTool, ToolName: "Read"},
			},
		}},
	}
	m := bareTv()
	m.c.m.verboseTranscript = true // list the trace's tools, not the run's summary
	m.transcript.entries = []transcript.Entry{sub}
	m.transcript.cursor = 0
	m.enterDetail()

	f := m.topFrame()
	if f.label != "default · Volta" {
		t.Errorf("breadcrumb label = %q, want nickname included", f.label)
	}
	if f.subagentName != "Volta" || f.subagentStatus != "closed" || f.subagentInput == "" {
		t.Fatalf("frame missing subagent identity: name=%q status=%q input=%q",
			f.subagentName, f.subagentStatus, f.subagentInput)
	}
	out := ansi.Strip(m.detailBody())
	if !strings.Contains(out, "Volta") {
		t.Errorf("expected nickname in drilled trace body, got:\n%s", out)
	}
	if !strings.Contains(out, "full task message") {
		t.Errorf("expected full input in drilled trace body, got:\n%s", out)
	}
	if !strings.Contains(out, "closed") {
		t.Errorf("expected status in drilled trace body (an expanded context), got:\n%s", out)
	}
	if !strings.Contains(out, "Read") {
		t.Errorf("expected the trace's own items still rendered, got:\n%s", out)
	}
}

// TestSubagentLabelStableAcrossExpand verifies the identity label stays stable
// across collapse/expand.
func TestSubagentLabelStableAcrossExpand(t *testing.T) {
	m := bareTv()
	it := transcript.Entry{
		Kind: transcript.EntrySubagent,
		Subagents: []transcript.Subagent{{
			Type:   "default",
			Name:   "Volta",
			Status: "closed",
		}},
	}
	collapsed := ansi.Strip(m.entryBlock(it, false, false, true, false, 200))
	expanded := ansi.Strip(m.entryBlock(it, true, false, true, false, 200))

	const label = "Spawn Agent: Volta (default)"
	collapsedCol := strings.Index(collapsed, label)
	expandedCol := strings.Index(expanded, label)
	if collapsedCol < 0 || expandedCol < 0 {
		t.Fatalf("label not found: collapsed=%q expanded=%q", collapsed, expanded)
	}
	if collapsedCol != expandedCol {
		t.Errorf("label column shifted: collapsed at %d, expanded at %d\ncollapsed: %q\nexpanded:  %q",
			collapsedCol, expandedCol, collapsed, expanded)
	}
	if strings.Contains(collapsed, "closed") {
		t.Errorf("collapsed row should not show status, got:\n%s", collapsed)
	}
	if !strings.Contains(expanded, "closed") {
		t.Errorf("expanded row should show status, got:\n%s", expanded)
	}
}

func TestActDetailDrill_HistoryNestedFetch(t *testing.T) {
	sub := transcript.Entry{
		Kind: transcript.EntrySubagent,
		// Trace empty => lazy
		Subagents: []transcript.Subagent{{Type: "Explore", ID: "B", HasTrace: true}},
	}
	mm := withView(testModel(), viewHistoryTranscript)
	m := tvOf(&mm)
	m.history.openNodeID, m.history.openPath = "n1", "/p/sess.jsonl"
	m.transcript.detailStack = []detailFrame{{
		items: []transcript.Entry{sub}, cursor: 0, expanded: map[int]bool{},
	}}
	cmd := m.actDetailDrill(tea.KeyPressMsg{})
	top := m.topFrame()
	if top == nil || top.agentID != "B" {
		t.Fatalf("expected pushed frame with agentID B, got %+v", top)
	}
	if cmd == nil {
		t.Fatal("expected a fetch command for history nested drill")
	}
}

func TestOpenByNameDrillsInTheCardDetail(t *testing.T) {
	sub := transcript.Entry{Kind: transcript.EntrySubagent, Subagents: []transcript.Subagent{{Type: "explorer", HasTrace: true,
		Trace: []transcript.Entry{{Kind: transcript.EntryTool, ToolName: "Read"}}}}}
	m := detailTestModel(traceFixture("Opus 4.8", transcript.Entry{Kind: transcript.EntryText, Text: "hi"}, sub))
	m.c.m.width, m.c.m.height = 80, 30
	m.handleDetailKey(tea.KeyPressMsg{Code: 'j'})
	m.handleDetailKey(cmdMsg("open"))
	if len(m.transcript.detailStack) != 2 || m.topFrame().label != "explorer" {
		t.Fatalf("open in the card detail must drill in: frames=%d", len(m.transcript.detailStack))
	}
}
