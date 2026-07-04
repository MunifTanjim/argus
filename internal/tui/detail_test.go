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
	m := testModel()
	m.toolBodies = map[string]toolBodyEntry{
		"loadingTool": {loading: true},
		"doneTool":    {done: true, toolInput: `{"x":1}`, result: "out", resultIsError: true},
	}

	// No ToolID → not addressable → treated as already resolved (renders inline).
	if _, fetched := m.filledTool(transcript.Item{Kind: transcript.ItemTool, ToolInput: "inline"}); !fetched {
		t.Error("item without ToolID should be reported as fetched")
	}
	// Outstanding fetch → not yet fetched (caller shows a placeholder).
	if _, fetched := m.filledTool(transcript.Item{Kind: transcript.ItemTool, ToolID: "loadingTool"}); fetched {
		t.Error("loading item should not be reported as fetched")
	}
	// Unknown id → not fetched.
	if _, fetched := m.filledTool(transcript.Item{Kind: transcript.ItemTool, ToolID: "unknown"}); fetched {
		t.Error("unknown tool should not be reported as fetched")
	}
	// Completed fetch → fields filled from the cache.
	got, fetched := m.filledTool(transcript.Item{Kind: transcript.ItemTool, ToolID: "doneTool"})
	if !fetched {
		t.Fatal("done item should be reported as fetched")
	}
	if got.ToolInput != `{"x":1}` || got.Result != "out" || !got.ResultIsError {
		t.Errorf("filled item = %+v", got)
	}
}

func detailTestModel(c transcript.Chunk) model {
	m := testModel()
	m.transcript.chunks = []transcript.Chunk{c}
	m.transcript.cursor = 0
	m.historyView = histDetail
	m.enterDetail()
	return m
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

func TestDetailItemsHaveAccentRule(t *testing.T) {
	m := detailTestModel(transcript.Chunk{
		ID: "a", Kind: transcript.ChunkAI, ModelName: "Opus 4.8",
		Items: []transcript.Item{
			{Kind: transcript.ItemText, Text: "hello output"},
			{Kind: transcript.ItemTool, ToolName: "Bash",
				ToolInput: `{"command":"ls"}`, Result: "out"},
		},
	})
	out := m.detailBody()
	if !strings.Contains(out, "┃") {
		t.Errorf("detail items should have an accent rule:\n%s", out)
	}
	if !strings.Contains(out, "hello output") {
		t.Errorf("output content lost:\n%s", out)
	}
}

func TestDetailBodyCentersOnWideTerminal(t *testing.T) {
	m := detailTestModel(transcript.Chunk{
		ID: "a", Kind: transcript.ChunkAI, ModelName: "Opus 4.8",
		Items: []transcript.Item{{Kind: transcript.ItemText, Text: "hi"}},
	})
	m.width = 200 // > maxContentWidth (160) → centerBlock adds a left gutter
	m.height = 40
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
	m := testModel()

	cases := map[string]transcript.Item{
		"bash command": {Kind: transcript.ItemTool, ToolName: "Bash",
			ToolInput: `{"command":"` + longCmd + `"}`},
		"json result": {Kind: transcript.ItemTool, ToolName: "UnknownTool",
			Result: longJSON},
		"edit diff": {Kind: transcript.ItemTool, ToolName: "Edit",
			ToolInput: `{"file_path":"a.go","old_string":"short","new_string":"` + strings.Repeat("z", 200) + `"}`},
	}
	for name, it := range cases {
		// Gutter adds 2 columns, so the wrapped body must stay within width.
		out := m.detailItemBody(it, itemAccentColor(it), GlyphAccentBar, width)
		if got := maxLineWidth(out); got > width {
			t.Errorf("%s: line width %d > %d:\n%s", name, got, width, out)
		}
	}
}

func TestCollapsedRowFitsWidth(t *testing.T) {
	m := testModel()
	it := transcript.Item{Kind: transcript.ItemTool, ToolName: "Bash",
		InputPreview: strings.Repeat("a long command preview ", 6)}
	out := m.detailRowBlock(it, false, false, 40)
	if got := maxLineWidth(out); got > 40 {
		t.Errorf("collapsed row width %d > 40:\n%s", got, out)
	}
	if n := strings.Count(out, "\n") + 1; n != 1 {
		t.Errorf("collapsed row should be a single line, got %d:\n%s", n, out)
	}
}

// The focused item uses a heavy gutter bar; an unfocused one uses the thin bar.
// This is the color-independent focus cue.
func TestDetailRowBarReflectsFocus(t *testing.T) {
	m := testModel()
	it := transcript.Item{Kind: transcript.ItemTool, ToolName: "Bash", InputPreview: "ls"}

	focused := m.detailRowBlock(it, false, true, 40)
	if !strings.Contains(focused, GlyphAccentBarFocused) {
		t.Errorf("focused row should use the heavy bar %q:\n%s", GlyphAccentBarFocused, focused)
	}
	unfocused := m.detailRowBlock(it, false, false, 40)
	if !strings.Contains(unfocused, GlyphAccentBar) {
		t.Errorf("unfocused row should use the thin bar %q:\n%s", GlyphAccentBar, unfocused)
	}
	if strings.Contains(unfocused, GlyphAccentBarFocused) {
		t.Errorf("unfocused row should not use the heavy bar:\n%s", unfocused)
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
	out := interactionBody(m, ix, width)
	if got := maxLineWidth(out); got > width {
		t.Errorf("permission body line width %d > %d:\n%s", got, width, out)
	}
}

func TestDetailScrollHint(t *testing.T) {
	// Build a chunk whose render far exceeds a tiny viewport.
	var items []transcript.Item
	for i := 0; i < 40; i++ {
		items = append(items, transcript.Item{Kind: transcript.ItemText, Text: "line of output"})
	}
	m := detailTestModel(transcript.Chunk{ID: "a", Kind: transcript.ChunkAI, Items: items})
	m.width, m.height = 80, 10

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

func TestFlattenTracePrependsPrompt(t *testing.T) {
	chunks := []transcript.Chunk{
		{Kind: transcript.ChunkUser, Text: "find the bug"},
		{Kind: transcript.ChunkAI, Items: []transcript.Item{{Kind: transcript.ItemTool, ToolName: "Grep"}}},
		{Kind: transcript.ChunkUser, Text: "keep going"},
	}
	items := flattenTrace(chunks)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Kind != transcript.ItemPrompt || items[0].Text != "find the bug" {
		t.Errorf("items[0] = %+v, want ItemPrompt %q", items[0], "find the bug")
	}
	if items[1].Kind != transcript.ItemTool {
		t.Errorf("items[1].Kind = %q, want tool", items[1].Kind)
	}
}

// Only chunk 0 is the prompt; a later user chunk (team-agent shape) is not hoisted.
func TestFlattenTraceUserChunkAfterAINotPrompt(t *testing.T) {
	chunks := []transcript.Chunk{
		{Kind: transcript.ChunkAI, Items: []transcript.Item{{Kind: transcript.ItemText, Text: "out"}}},
		{Kind: transcript.ChunkUser, Text: "later message"},
	}
	items := flattenTrace(chunks)
	if len(items) != 1 || items[0].Kind != transcript.ItemText {
		t.Fatalf("got %+v, want single text item (no prompt)", items)
	}
}

func TestFlattenTraceBlankPromptSkipped(t *testing.T) {
	chunks := []transcript.Chunk{
		{Kind: transcript.ChunkUser, Text: "  \n "},
		{Kind: transcript.ChunkAI, Items: []transcript.Item{{Kind: transcript.ItemText, Text: "out"}}},
	}
	items := flattenTrace(chunks)
	if len(items) != 1 || items[0].Kind != transcript.ItemText {
		t.Fatalf("got %+v, want single text item (blank prompt skipped)", items)
	}
}

func TestFlattenTraceNoUserChunk(t *testing.T) {
	chunks := []transcript.Chunk{
		{Kind: transcript.ChunkAI, Items: []transcript.Item{{Kind: transcript.ItemText, Text: "out"}}},
	}
	items := flattenTrace(chunks)
	if len(items) != 1 || items[0].Kind != transcript.ItemText {
		t.Fatalf("got %+v, want single text item", items)
	}
}

func TestEnterDrillPopStack(t *testing.T) {
	sub := transcript.Item{
		Kind: transcript.ItemSubagent,
		Subagents: []transcript.Subagent{{Type: "explorer", HasTrace: true,
			Trace: []transcript.Chunk{{Kind: transcript.ChunkAI, Items: []transcript.Item{
				{Kind: transcript.ItemTool, ToolName: "Read"},
				{Kind: transcript.ItemTool, ToolName: "Grep"},
			}}},
		}},
	}
	m := testModel()
	m.transcript.chunks = []transcript.Chunk{{
		ID: "a", Kind: transcript.ChunkAI, ModelName: "Opus 4.8",
		Items: []transcript.Item{{Kind: transcript.ItemText, Text: "hi"}, sub},
	}}
	m.transcript.cursor = 0
	m.enterDetail()
	if len(m.transcript.detailStack) != 1 || len(m.topFrame().items) != 2 {
		t.Fatalf("root frame: %d frames", len(m.transcript.detailStack))
	}
	// Drill into the subagent (cursor on item 1).
	m.topFrame().cursor = 1
	m.drillDetail()
	if len(m.transcript.detailStack) != 2 || len(m.topFrame().items) != 2 || m.topFrame().label != "explorer" {
		t.Fatalf("drill: frames=%d label=%q", len(m.transcript.detailStack), m.topFrame().label)
	}
	if m.topFrame().defaultExpanded {
		t.Error("drilled subagent children should start collapsed")
	}
	if m.popDetail() {
		t.Error("popping to root should not empty the stack")
	}
	if !m.popDetail() {
		t.Error("popping the root should empty the stack")
	}
}

func TestDetailRowBlockCollapsedVsExpanded(t *testing.T) {
	m := testModel()
	it := transcript.Item{Kind: transcript.ItemTool, ToolName: "Bash",
		ToolInput: `{"command":"ls"}`, Result: "out"}

	collapsed := m.detailRowBlock(it, false, false, 60)
	if strings.Contains(collapsed, "out") {
		t.Errorf("collapsed row should not show the result body:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "Bash") {
		t.Errorf("collapsed row should name the tool:\n%s", collapsed)
	}
	expanded := m.detailRowBlock(it, true, false, 60)
	if !strings.Contains(expanded, "out") {
		t.Errorf("expanded row should show the result body:\n%s", expanded)
	}

	sub := transcript.Item{Kind: transcript.ItemSubagent, Subagents: []transcript.Subagent{{Type: "explorer", HasTrace: true,
		Trace: []transcript.Chunk{{Kind: transcript.ChunkAI, Items: []transcript.Item{{Kind: transcript.ItemTool, ToolName: "Read"}}}}}}}
	if !strings.Contains(m.detailRowBlock(sub, false, false, 60), "↵") {
		t.Errorf("a drillable subagent row should show the drill affordance")
	}
}

func TestDetailBodyShowsBreadcrumbAndRows(t *testing.T) {
	m := detailTestModel(transcript.Chunk{
		ID: "a", Kind: transcript.ChunkAI, ModelName: "Opus 4.8",
		Items: []transcript.Item{
			{Kind: transcript.ItemText, Text: "hello"},
			{Kind: transcript.ItemTool, ToolName: "Bash", ToolInput: `{"command":"ls"}`},
		},
	})
	m.width, m.height = 80, 30
	// Output (text) items start pre-expanded; other root items start collapsed.
	if !m.topFrame().isExpanded(0) {
		t.Errorf("Output item should start pre-expanded")
	}
	if m.topFrame().isExpanded(1) {
		t.Errorf("non-Output root items should start collapsed")
	}
	out := m.detailBody()
	if !strings.Contains(out, "Opus 4.8") {
		t.Errorf("breadcrumb missing root label:\n%s", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "Bash") {
		t.Errorf("frame rows missing:\n%s", out)
	}
	// Drill into a focused item → breadcrumb grows.
	m.topFrame().cursor = 1
	m.drillDetail()
	if out := m.detailBody(); !strings.Contains(out, "Opus 4.8 › Bash") {
		t.Errorf("drilled breadcrumb missing:\n%s", out)
	}
}

// A non-drillable item focuses exactly once; further Enter presses on the focus
// frame must not keep nesting the same item.
func TestFocusFrameDoesNotRenest(t *testing.T) {
	m := detailTestModel(transcript.Chunk{
		ID: "a", Kind: transcript.ChunkAI, ModelName: "Opus 4.8",
		Items: []transcript.Item{
			{Kind: transcript.ItemTool, ToolName: "Bash", ToolInput: `{"command":"ls"}`},
		},
	})
	m.width, m.height = 80, 30

	m.drillDetail()
	if len(m.transcript.detailStack) != 2 || !m.topFrame().focused {
		t.Fatalf("first drill should focus once: frames=%d focused=%v", len(m.transcript.detailStack), m.topFrame().focused)
	}
	for i := 0; i < 3; i++ {
		m.drillDetail() // re-pressing Enter must be a no-op on a focus frame
	}
	if len(m.transcript.detailStack) != 2 {
		t.Fatalf("focus frame re-nested: frames=%d", len(m.transcript.detailStack))
	}
}

func TestDrillableUsesHasTrace(t *testing.T) {
	withTrace := transcript.Item{Kind: transcript.ItemSubagent, Subagents: []transcript.Subagent{{ID: "a1", HasTrace: true}}}
	if !drillable(withTrace) {
		t.Error("subagent with HasTrace should be drillable")
	}
	plain := transcript.Item{Kind: transcript.ItemTool, ToolName: "Read"}
	if drillable(plain) {
		t.Error("non-subagent should not be drillable")
	}
}

func TestDetailKeyNav(t *testing.T) {
	sub := transcript.Item{Kind: transcript.ItemSubagent, Subagents: []transcript.Subagent{{Type: "explorer", HasTrace: true,
		Trace: []transcript.Chunk{{Kind: transcript.ChunkAI, Items: []transcript.Item{
			{Kind: transcript.ItemTool, ToolName: "Read"}}}}}}}
	m := detailTestModel(transcript.Chunk{ID: "a", Kind: transcript.ChunkAI,
		Items: []transcript.Item{{Kind: transcript.ItemText, Text: "hi"}, sub}})
	m.width, m.height = 80, 30

	res, _ := m.handleDetailKey(tea.KeyPressMsg{Code: 'j'})
	m = res.(model)
	if m.topFrame().cursor != 1 {
		t.Fatalf("cursor=%d want 1", m.topFrame().cursor)
	}
	// space toggles expansion of the selected item (root items start collapsed,
	// so the first space expands).
	before := m.topFrame().isExpanded(1)
	res, _ = m.handleDetailKey(tea.KeyPressMsg{Code: ' '})
	m = res.(model)
	if m.topFrame().isExpanded(1) == before {
		t.Error("space should toggle the selected item's expansion")
	}
	res, _ = m.handleDetailKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if len(m.transcript.detailStack) != 2 || m.topFrame().label != "explorer" {
		t.Fatalf("enter should drill: frames=%d", len(m.transcript.detailStack))
	}
}

func TestDetailSubagentShowsNicknameAndInput(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemSubagent,
		Subagents: []transcript.Subagent{{
			Type:   "default",
			Name:   "Volta",
			Desc:   "This is the full task message given to the subagent and it is quite long",
			Status: "closed",
		}},
	}
	out := m.detailItemBody(it, itemAccentColor(it), GlyphAccentBar, 200)
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
	sub := transcript.Item{
		Kind: transcript.ItemSubagent,
		Subagents: []transcript.Subagent{{
			Type: "default", Name: "Volta", Status: "closed",
			Desc:     "the full task message given to the subagent",
			HasTrace: true,
			Trace: []transcript.Chunk{{Kind: transcript.ChunkAI, Items: []transcript.Item{
				{Kind: transcript.ItemTool, ToolName: "Read"},
			}}},
		}},
	}
	m := testModel()
	m.transcript.chunks = []transcript.Chunk{{
		ID: "a", Kind: transcript.ChunkAI, ModelName: "Opus 4.8",
		Items: []transcript.Item{sub},
	}}
	m.transcript.cursor = 0
	m.enterDetail()
	m.drillDetail()

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
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemSubagent,
		Subagents: []transcript.Subagent{{
			Type:   "default",
			Name:   "Volta",
			Status: "closed",
		}},
	}
	collapsed := ansi.Strip(m.detailRowBlock(it, false, false, 200))
	expanded := ansi.Strip(m.detailRowBlock(it, true, false, 200))

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
	sub := transcript.Item{
		Kind: transcript.ItemSubagent,
		// Trace empty => lazy
		Subagents: []transcript.Subagent{{Type: "Explore", ID: "B", HasTrace: true}},
	}
	m := testModel()
	m.mode = modeHistoryTranscript
	m.history.openNodeID, m.history.openPath = "n1", "/p/sess.jsonl"
	m.transcript.detailStack = []detailFrame{{
		items: []transcript.Item{sub}, cursor: 0, expanded: map[int]bool{},
	}}
	mm, cmd := m.actDetailDrill(tea.KeyPressMsg{})
	got := mm.(model)
	top := got.topFrame()
	if top == nil || top.agentID != "B" {
		t.Fatalf("expected pushed frame with agentID B, got %+v", top)
	}
	if cmd == nil {
		t.Fatal("expected a fetch command for history nested drill")
	}
}
