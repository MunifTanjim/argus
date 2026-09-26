package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func seqModel() model {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{
		"global":   {"g.": "toggle show-hidden"},
		"projects": {"g": "goto top", "yy": "toggle show-gone", "<Space>x": "toggle left-sidebar"},
	})
	m.width, m.height = 120, 30
	m.projects.cursor = 2
	return m
}

func TestSequenceRunsItsCommand(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("g"))
	if m.projects.cursor != 2 {
		t.Fatal("g must wait while g. can follow")
	}
	m, _ = upd(m, keyMsg("."))
	if !m.projects.showHidden || m.projects.cursor != 2 {
		t.Errorf("g. should toggle hidden projects only: hidden=%v cursor=%d", m.projects.showHidden, m.projects.cursor)
	}
}

func TestTimeoutRunsTheShortKey(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("g"))
	m, _ = upd(m, keyTimeoutMsg{gen: m.keyGen})
	if m.projects.cursor != 0 || m.projects.showHidden {
		t.Errorf("after the timeout g goes to the top: cursor=%d hidden=%v", m.projects.cursor, m.projects.showHidden)
	}
}

func TestOtherKeyRunsTheShortKeyThenItself(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("g"))
	m, _ = upd(m, keyMsg("j"))
	if m.projects.cursor != 1 {
		t.Errorf("g then j: top, then down one row; cursor=%d", m.projects.cursor)
	}
}

func TestEscCancelsASequence(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("g"))
	m, _ = upd(m, keyMsg("esc"))
	m, _ = upd(m, keyTimeoutMsg{gen: m.keyGen})
	if len(m.keyBuf) != 0 || m.projects.cursor != 2 {
		t.Errorf("esc drops the pending g: buf=%d cursor=%d", len(m.keyBuf), m.projects.cursor)
	}
}

func TestStaleTimerDoesNothing(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("g"))
	old := m.keyGen
	m, _ = upd(m, keyMsg("."))
	m, _ = upd(m, keyTimeoutMsg{gen: old})
	if m.projects.cursor != 2 {
		t.Errorf("an old timer must not run g: cursor=%d", m.projects.cursor)
	}
}

func TestPrefixOnlyTimeoutDropsTheKeys(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("y"))
	m, _ = upd(m, keyTimeoutMsg{gen: m.keyGen})
	if len(m.keyBuf) != 0 || m.projects.showGone {
		t.Error("a lone y that only starts yy is dropped")
	}
	m, _ = upd(m, keyMsg("y"))
	m, _ = upd(m, keyMsg("y"))
	if !m.projects.showGone {
		t.Error("yy should toggle gone projects")
	}
}

func TestSequenceStartingWithSpace(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg(" "))
	m, _ = upd(m, keyMsg("x"))
	if m.sidebarVisible() == seqModel().sidebarVisible() {
		t.Error("<Space>x should toggle the left sidebar")
	}
}

func TestKeyAfterSequenceGoesToTheOpenedInput(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("g"))
	m, _ = upd(m, keyMsg("/"))
	m, _ = upd(m, keyMsg("g"))
	if !m.inputActive() || m.projects.input.Value() != "g" || m.projects.cursor != 0 {
		t.Errorf("g runs, / opens the filter, and g is typed: input=%v value=%q cursor=%d", m.inputActive(), m.projects.input.Value(), m.projects.cursor)
	}
}

func TestCtrlCQuitsWhileASequenceWaits(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("g"))
	if _, cmd := m.Update(ctrlKey('c')); !quits(cmd) {
		t.Fatal("ctrl+c should quit")
	}
}

func TestFooterShowsThePendingKeys(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("g"))
	f := ansi.Strip(m.View().Content)
	if !strings.Contains(f, "g…") || !strings.Contains(f, ". toggle show-hidden") || !strings.Contains(f, "(wait) goto top") {
		t.Errorf("footer should show the pending g and what can follow:\n%s", f)
	}
}

// TestRestKeyAfterNewWaitIsNotLost: g a w runs g, then a starts av and waits,
// then w breaks av; w must still run.
func TestRestKeyAfterNewWaitIsNotLost(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{
		"projects": {"g": "goto top", "gab": "toggle show-gone", "av": "toggle show-hidden"},
	})
	m.width, m.height = 120, 30
	m.projects.cursor = 2
	m, _ = upd(m, keyMsg("g"))
	m, _ = upd(m, keyMsg("a"))
	m, _ = upd(m, keyMsg("w"))
	if m.projects.cursor != 0 || m.projects.showGone || m.projects.showHidden || len(m.keyBuf) != 0 {
		t.Errorf("g runs goto top; gab, av not completed; w unbound: cursor=%d gone=%v hidden=%v keyBuf=%d",
			m.projects.cursor, m.projects.showGone, m.projects.showHidden, len(m.keyBuf))
	}
}

// rawModeModel maps /x so that / waits (longer sequence), then on mismatch
// the flush runs / (filter-projects, opens an empty input) before the rest key.
func rawModeModel() model {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{
		"projects": {"/x": "toggle show-hidden", "yy": "toggle show-gone"},
	})
	m.width, m.height = 120, 30
	m.projects.cursor = 2
	return m
}

func TestRawModeAfterMatchGetsRestKeysInOrder(t *testing.T) {
	m := rawModeModel()
	m, _ = upd(m, keyMsg("/")) // / waits: /x is longer, / is filter-projects (exact match)
	m, _ = upd(m, keyMsg("y")) // y mismatches, / runs (filter opens, empty input), y goes to input
	m, _ = upd(m, keyMsg("o")) // raw: o goes to input
	if !m.inputActive() || m.projects.input.Value() != "yo" || len(m.keyBuf) != 0 {
		t.Errorf("/ opens filter, y and o type in order: active=%v value=%q keyBuf=%d",
			m.inputActive(), m.projects.input.Value(), len(m.keyBuf))
	}
}

func TestPendingKeyDroppedWhenStateGoesRaw(t *testing.T) {
	m := seqModel()
	m, _ = upd(m, keyMsg("g")) // g waits
	gen := m.keyGen
	m.projects.offerSpawn = &spawnOffer{nodeID: "n1"} // inject a raw state
	m, _ = upd(m, keyTimeoutMsg{gen: gen})
	if m.projects.offerSpawn == nil || m.projects.cursor != 2 || len(m.keyBuf) != 0 {
		t.Errorf("pending g must be dropped when state turns raw: offer=%v cursor=%d keyBuf=%d",
			m.projects.offerSpawn, m.projects.cursor, len(m.keyBuf))
	}
}

func TestMismatchMultiKeyDropsEarliestAndRunsLast(t *testing.T) {
	m := seqModel()
	// y starts yy (longer), then j mismatches; y is dropped, j runs next.
	m, _ = upd(m, keyMsg("y"))
	m, _ = upd(m, keyMsg("j"))
	if m.projects.cursor != 3 {
		t.Errorf("y dropped, j moves cursor down from 2 to 3; cursor=%d", m.projects.cursor)
	}
}

// Uses "gj" → next (valid on history) so g waits while gj can follow.
func TestHistoryProjectsFooterShowsPendingKeys(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{
		"global": {"gj": "next"},
	})
	m.width, m.height = 120, 30
	m.mode = modeHistoryProjects
	m, _ = upd(m, keyMsg("g")) // g waits: gj is longer, g is goto top (exact match)
	f := ansi.Strip(m.View().Content)
	if !strings.Contains(f, "g…") {
		t.Errorf("history-projects footer should show pending g:\n%s", f)
	}
}

func TestKeyAfterOpeningTheLiveScreenGoesToThePane(t *testing.T) {
	m := testModel()
	m.mode, m.selectedID = modeSession, "s1"
	m.sessions = map[string]session.Session{"s1": {ID: "s1", CanOpenTerminal: true}}
	m = withKeymap(m, map[string]map[string]string{"session": {"zz": "open live-screen", "zzy": "fold close"}})
	m, _ = upd(m, keyMsg("z"))
	m, _ = upd(m, keyMsg("z"))
	m, _ = upd(m, keyMsg("j"))
	defer m.leaveScreen()
	if m.mode != modeScreen || len(m.termKeyCh) != 1 {
		t.Errorf("zz opens the live screen and j goes to the pane: mode=%v queued=%d", m.mode, len(m.termKeyCh))
	}
}

func TestHiddenTreeKeyRunsAfterOneTimeout(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"projects": {"g": "goto top", "gz": "toggle show-gone"}})
	m.width, m.height = 120, 30
	m.projects.sidebarHidden = true
	m.projects.focus = focusTree
	m.projects.selectRow("n1:w1")
	m, _ = upd(m, keyMsg("g"))
	m, _ = upd(m, keyTimeoutMsg{gen: m.keyGen})
	if len(m.keyBuf) != 0 || m.projects.focus != focusPane {
		t.Errorf("g runs in the pane after one timeout: keyBuf=%d focus=%v", len(m.keyBuf), m.projects.focus)
	}
}
