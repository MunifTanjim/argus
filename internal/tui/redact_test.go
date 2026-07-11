package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/bundle"
)

func newRedactModel() model {
	m := model{mode: modeHistoryTranscript, historyView: histTranscript, viewer: true, redactMode: true}
	m.history.openPath = "/x/s.jsonl"
	return m
}

func TestRedactInputQueuesLiteral(t *testing.T) {
	m := newRedactModel()

	// 'd' opens the input.
	res, _ := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = res.(model)
	if !m.redact.inputActive {
		t.Fatal("d should open the redact input")
	}

	// Type "sk".
	for _, r := range "sk" {
		res, _ = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
	}
	if m.redact.input != "sk" {
		t.Fatalf("input buffer = %q, want sk", m.redact.input)
	}

	// Enter commits.
	res, _ = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.redact.inputActive {
		t.Fatal("enter should close the input")
	}
	if len(m.redact.literals) != 1 || m.redact.literals[0] != "sk" {
		t.Fatalf("literals = %v, want [sk]", m.redact.literals)
	}
}

func TestRedactInputWorksInDetailView(t *testing.T) {
	// Queued literals apply bundle-wide, so redaction keys must stay live after
	// drilling into the detail view, not just on the transcript page.
	m := newRedactModel()
	m.historyView = histDetail

	res, _ := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = res.(model)
	if !m.redact.inputActive {
		t.Fatal("d should open the redact input in the detail view")
	}

	// A drill-down navigation key must still reach the detail handler when the
	// redact input isn't capturing (i.e. redaction doesn't swallow detail nav).
	m.redact.inputActive = false
	if m.redactActive() && func() bool {
		_, _, ok := m.handleRedactKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
		return ok
	}() {
		t.Fatal("j must fall through to detail navigation, not be consumed by redaction")
	}
}

func TestRedactInputEscCancels(t *testing.T) {
	m := newRedactModel()
	res, _ := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = res.(model)
	res, _ = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = res.(model)
	if m.redact.inputActive || len(m.redact.literals) != 0 {
		t.Fatal("esc should cancel input without queuing")
	}
}

func TestRedactKeysInertWithoutFlag(t *testing.T) {
	m := newRedactModel()
	m.redactMode = false
	res, _ := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if res.(model).redact.inputActive {
		t.Fatal("d must not open redact input when --redact is off")
	}
}

func writeMinimalExtraction(t *testing.T, dir string) {
	t.Helper()
	man := `{"format_version":1,"agent":"claude","entry":"root/s.jsonl","metadata":{"title":"t"}}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"), []byte(`{"r":"sk-secret"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRedactSaveWritesSiblingBundle(t *testing.T) {
	src := t.TempDir()
	writeMinimalExtraction(t, src)

	m := newRedactModel()
	m.redactSrcDir = src
	m.bundlePath = filepath.Join(t.TempDir(), "session.argus")
	m.redact.literals = []string{"sk-secret"}

	res, cmd := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'W', Text: "W"})
	m = res.(model)
	if cmd == nil {
		t.Fatal("W with queued literals should run a prepare command")
	}
	msg := cmd()
	res, _ = m.Update(msg)
	m = res.(model)
	if !m.redact.pendingSave {
		t.Fatal("prepare result should arm pendingSave")
	}

	// The final -redacted name must not exist until the user confirms; the work is
	// staged to a temp file so an unacknowledged residual never lands under it.
	wantPath := filepath.Join(filepath.Dir(m.bundlePath), "session-redacted.argus")
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("final bundle must not exist before confirmation, stat err = %v", err)
	}
	if m.redact.tempPath == "" {
		t.Fatal("prepare should stage a temp bundle path")
	}
	if _, err := os.Stat(m.redact.tempPath); err != nil {
		t.Fatalf("staged temp bundle should exist: %v", err)
	}

	res, cmd = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = res.(model)
	if cmd == nil {
		t.Fatal("y should run the save command")
	}
	done := cmd().(redactDoneMsg)
	if done.err != nil {
		t.Fatalf("save failed: %v", done.err)
	}
	if _, err := os.Stat(done.path); err != nil {
		t.Fatalf("redacted bundle not written: %v", err)
	}
	if filepath.Base(done.path) != "session-redacted.argus" {
		t.Fatalf("unexpected output name: %s", done.path)
	}
}

// writeExtractionWithBinarySecret builds an extraction whose secret lives in a
// binary file, so RedactTree can't scrub it and must warn.
func writeExtractionWithBinarySecret(t *testing.T, dir, secret string) {
	t.Helper()
	man := `{"format_version":1,"agent":"claude","entry":"root/s.jsonl","metadata":{"title":"t"}}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"), []byte(`{"r":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A NUL byte in the head marks this binary; the secret rides in the tail.
	blob := append([]byte{0x00, 0x01, 0x02}, []byte(secret)...)
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRedactSaveWritesWarningsSidecar(t *testing.T) {
	src := t.TempDir()
	writeExtractionWithBinarySecret(t, src, "sk-secret")

	m := newRedactModel()
	m.redactSrcDir = src
	m.bundlePath = filepath.Join(t.TempDir(), "session.argus")
	m.redact.literals = []string{"sk-secret"}

	// Prepare: RedactTree finds the un-scrubbable binary secret and arms warnConfirm.
	res, cmd := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'W', Text: "W"})
	m = res.(model)
	res, _ = m.Update(cmd())
	m = res.(model)
	if !m.redact.pendingSave || !m.redact.warnConfirm {
		t.Fatalf("prepare should arm warnConfirm for the binary secret, got %+v", m.redact)
	}

	// First y acknowledges the warning, second y commits.
	res, cmd = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = res.(model)
	if cmd != nil {
		t.Fatal("first y should only acknowledge, not save")
	}
	_, cmd = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("second y should run the save")
	}
	done := cmd().(redactDoneMsg)
	if done.err != nil {
		t.Fatalf("save failed: %v", done.err)
	}
	if done.sidecar == "" {
		t.Fatal("warnings should produce a sidecar path")
	}
	body, err := os.ReadFile(done.sidecar)
	if err != nil {
		t.Fatalf("sidecar not written: %v", err)
	}
	if !contains(string(body), "blob.bin") {
		t.Fatalf("sidecar should name the leaking file, got:\n%s", body)
	}
}

func TestRedactFooterStates(t *testing.T) {
	m := newRedactModel()

	// Idle with a queued literal: shows count + key hint.
	m.redact.literals = []string{"a"}
	if got := m.redactFooter("BASE"); got == "BASE" {
		t.Fatal("expected redact hint, got base footer")
	}

	// Input active: shows the buffer.
	m.redact.inputActive, m.redact.input = true, "sk-x"
	if got := m.redactFooter("BASE"); !contains(got, "sk-x") {
		t.Fatalf("input footer should echo buffer, got %q", got)
	}
	m.redact.inputActive = false

	// Pending save: shows confirm.
	rep := bundle.Report{Counts: map[string]int{"a": 2}}
	m.redact.report = &rep
	m.redact.pendingSave = true
	if got := m.redactFooter("BASE"); !contains(got, "y/n") {
		t.Fatalf("confirm footer should ask y/n, got %q", got)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestRedactFooterFlashSurfaces(t *testing.T) {
	m := newRedactModel()
	m.redact.literals = []string{"a"}
	m.flash = "redacted: /tmp/x-redacted.argus"

	// Flash must win over the queued-count hint.
	if got := m.redactFooter("SENTINEL_BASE"); got != "SENTINEL_BASE" {
		t.Fatalf("flash should surface via base, got %q", got)
	}

	// Pending-save modal must still win over flash.
	rep := bundle.Report{Counts: map[string]int{"a": 1}}
	m.redact.report = &rep
	m.redact.pendingSave = true
	if got := m.redactFooter("SENTINEL_BASE"); !contains(got, "y/n") {
		t.Fatalf("confirm modal must beat flash, got %q", got)
	}
}

func TestRedactWarnConfirmTwoStep(t *testing.T) {
	m := newRedactModel()
	m.bundlePath = filepath.Join(t.TempDir(), "s.argus")
	m.redact.literals = []string{"sk-secret"}
	rep := bundle.Report{
		Counts:   map[string]int{"sk-secret": 1},
		Warnings: []string{"secret in binary file root/blob.bin (cannot redact)"},
	}

	res, _ := m.Update(redactPreparedMsg{report: rep, outPath: m.bundlePath + ".tmp"})
	m = res.(model)
	if !m.redact.pendingSave || !m.redact.warnConfirm {
		t.Fatalf("prepare with warnings should arm pendingSave+warnConfirm, got %+v", m.redact)
	}

	// First 'y' only acknowledges the warning — no save command yet.
	res, cmd := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = res.(model)
	if cmd != nil {
		t.Fatal("first y should acknowledge the warning, not save")
	}
	if m.redact.warnConfirm || !m.redact.pendingSave {
		t.Fatalf("first y should clear warnConfirm but keep pendingSave, got %+v", m.redact)
	}

	// Second 'y' performs the save.
	_, cmd = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("second y should run the save command")
	}
}

func TestRedactWarnConfirmCancels(t *testing.T) {
	m := newRedactModel()
	m.bundlePath = filepath.Join(t.TempDir(), "s.argus")
	m.redact.literals = []string{"sk-secret"}

	// A real staged temp file stands in for the prepared bundle; cancelling must
	// delete it so a partially-redacted copy isn't left behind.
	staged := filepath.Join(t.TempDir(), ".argus-redacted-stage")
	if err := os.WriteFile(staged, []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := bundle.Report{Counts: map[string]int{"sk-secret": 1}, Warnings: []string{"w"}}
	res, _ := m.Update(redactPreparedMsg{report: rep, tempPath: staged, outPath: m.bundlePath})
	m = res.(model)

	// Any non-y key cancels the whole flow at the warning stage.
	res, cmd := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = res.(model)
	if m.redact.pendingSave || m.redact.warnConfirm {
		t.Fatalf("n should cancel both flags, got %+v", m.redact)
	}
	if cmd == nil {
		t.Fatal("cancel should return a cleanup command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("abort command should produce no message, got %v", msg)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged temp bundle should be deleted on cancel, stat err = %v", err)
	}
}

func TestRedactFooterWarnConfirm(t *testing.T) {
	m := newRedactModel()
	rep := bundle.Report{Counts: map[string]int{"a": 1}, Warnings: []string{"w1", "w2"}}
	m.redact.report = &rep
	m.redact.pendingSave = true
	m.redact.warnConfirm = true

	got := m.redactFooter("BASE")
	if contains(got, "y/n") {
		t.Fatalf("warn stage must not show the plain y/n confirm, got %q", got)
	}
	if !contains(got, "remain") {
		t.Fatalf("warn stage must warn that secrets remain, got %q", got)
	}
}

func TestRedactDoneSurfacesWarnings(t *testing.T) {
	m := newRedactModel()
	res, _ := m.Update(redactDoneMsg{path: "/tmp/x-redacted.argus", warnings: []string{"w1", "w2"}})
	m = res.(model)
	if !contains(m.flash, "warning") {
		t.Fatalf("done flash should surface warnings, got %q", m.flash)
	}
}

func TestRedactListBodyRendersQueuedLiterals(t *testing.T) {
	m := newRedactModel()
	m.redact.literals = []string{"sk-supersecret", "ghp-token"}

	// Opening the list (D) must actually show the queued secrets, not just a count.
	res, _ := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'D', Text: "D"})
	m = res.(model)
	if !m.redactListActive() {
		t.Fatal("D should activate the redaction list")
	}

	body := m.redactListBody()
	if !contains(body, "queued redactions (2)") {
		t.Fatalf("list body should show a heading with the count, got:\n%s", body)
	}
	// Literals are shown in full so the user can tell them apart and manage them.
	if !contains(body, "sk-supersecret") || !contains(body, "ghp-token") {
		t.Fatalf("list body should show the queued literals in full, got:\n%s", body)
	}
	if !contains(body, "▸") {
		t.Fatalf("list body should mark the cursor row, got:\n%s", body)
	}
}

func TestRedactListBodyEmptyState(t *testing.T) {
	m := newRedactModel()
	res, _ := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'D', Text: "D"})
	m = res.(model)
	if got := m.redactListBody(); !contains(got, "no redactions queued") {
		t.Fatalf("empty list should explain how to add, got %q", got)
	}
}

func TestRedactAddFromListReturnsToList(t *testing.T) {
	m := newRedactModel()

	// Open the (empty) list, then press d to add — the empty-state hint promises this.
	res, _ := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'D', Text: "D"})
	m = res.(model)
	res, _ = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = res.(model)
	if !m.redact.inputActive || m.redact.listActive {
		t.Fatalf("d on the list should open the input and hide the list, got %+v", m.redact)
	}

	for _, r := range "sk-x" {
		res, _ = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
	}
	res, _ = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)

	if len(m.redact.literals) != 1 || m.redact.literals[0] != "sk-x" {
		t.Fatalf("literal not queued: %v", m.redact.literals)
	}
	if !m.redact.listActive || m.redact.inputActive {
		t.Fatalf("after adding, should return to the list, got %+v", m.redact)
	}
	if m.redact.listCursor != 0 {
		t.Fatalf("cursor should land on the new entry, got %d", m.redact.listCursor)
	}
}

func TestRedactListDelete(t *testing.T) {
	m := newRedactModel()
	m.redact.literals = []string{"aaa", "bbb", "ccc"}

	// Open the list.
	res, _ := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'D', Text: "D"})
	m = res.(model)
	if !m.redact.listActive {
		t.Fatal("D should open the redaction list")
	}

	// Move to index 1 and delete.
	res, _ = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = res.(model)
	res, _ = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = res.(model)
	if len(m.redact.literals) != 2 || m.redact.literals[1] != "ccc" {
		t.Fatalf("delete failed: %v", m.redact.literals)
	}

	// Esc closes.
	res, _ = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = res.(model)
	if m.redact.listActive {
		t.Fatal("esc should close the list")
	}
}
