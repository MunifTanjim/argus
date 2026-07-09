package tui

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/charmbracelet/x/vt"
)

// A device-attributes query (ESC [ c) in the PTY stream makes the emulator write a
// reply to its internal io.Pipe, which blocks until drained. enterScreen must start
// a drain goroutine; otherwise applyEvent (and the whole UI) deadlocks on it.
func TestScreenAttachDrainsEmulatorReplies(t *testing.T) {
	c := &recordingClient{}
	m := testModel()
	m.client = c
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Tmux: session.TmuxLocation{PaneID: "%0"}}}
	m.mode = modeList
	m2, _ := m.enterScreen("s1") // starts the drain goroutine

	data := base64.StdEncoding.EncodeToString([]byte("hi\x1b[c\x1b[>c\x1b[6n more"))
	params, _ := json.Marshal(api.TerminalOutput{TermID: m2.termID, Data: data})
	done := make(chan struct{})
	go func() {
		m2.applyEvent(api.Notification{Method: api.MethodTerminalOutput, Params: params})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("applyEvent hung on a device-attributes query: emulator reply pipe not drained")
	}
	if !strings.Contains(m2.term.Render(), "hi") {
		t.Errorf("render missing written text; got %q", m2.term.Render())
	}
	_, _ = m2.leaveScreen() // ends the drain goroutine
}

func TestTerminalExitedLeavesScreen(t *testing.T) {
	m, _ := screenModel() // attached: mode=screen, termID="s1", origin=list

	// exited for another term is ignored.
	other, _ := json.Marshal(api.TerminalExited{TermID: "other"})
	m.applyEvent(api.Notification{Method: api.MethodTerminalExited, Params: other})
	if m.mode != modeScreen {
		t.Fatalf("exited(other): mode=%v want screen (ignored)", m.mode)
	}

	// exited for the active term leaves the dead attach and flashes.
	params, _ := json.Marshal(api.TerminalExited{TermID: m.termID})
	m.applyEvent(api.Notification{Method: api.MethodTerminalExited, Params: params})
	if m.mode != modeList || m.term != nil || m.termID != "" {
		t.Fatalf("exited: mode=%v term=%v id=%q want list + cleared", m.mode, m.term, m.termID)
	}
	if m.flash != "terminal exited" {
		t.Errorf("exited: flash=%q want %q", m.flash, "terminal exited")
	}
}

func TestPtyBytesFor(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want []byte
	}{
		{"printable", tea.KeyPressMsg{Code: 'a', Text: "a"}, []byte("a")},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, []byte{'\r'}},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, []byte{'\t'}},
		{"backspace", tea.KeyPressMsg{Code: tea.KeyBackspace}, []byte{0x7f}},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEsc}, []byte{0x1b}},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, []byte("\x1b[A")},
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, []byte("\x1b[B")},
		{"right", tea.KeyPressMsg{Code: tea.KeyRight}, []byte("\x1b[C")},
		{"left", tea.KeyPressMsg{Code: tea.KeyLeft}, []byte("\x1b[D")},
		{"space", tea.KeyPressMsg{Code: tea.KeySpace}, []byte{' '}},
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, []byte{0x03}},
		{"ctrl+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, []byte{0x01}},
		{"f1", tea.KeyPressMsg{Code: tea.KeyF1}, []byte("\x1bOP")},
		// Alt+Enter / Shift+Enter → ESC+CR (meta-Enter): "insert newline" in agent TUIs.
		{"alt+enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}, []byte("\x1b\r")},
		{"shift+enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, []byte("\x1b\r")},
		{"plain enter stays CR", tea.KeyPressMsg{Code: tea.KeyEnter}, []byte{'\r'}},
		// Modified cursor/nav keys: xterm CSI encoding. Cursor keys use CSI 1;<mod><final>;
		// the ~-style nav keys use CSI <num>;<mod>~. mod = 1 + shift(1) + alt(2) + ctrl(4).
		{"ctrl+home", tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl}, []byte("\x1b[1;5H")},
		{"ctrl+end", tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl}, []byte("\x1b[1;5F")},
		{"ctrl+up", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl}, []byte("\x1b[1;5A")},
		{"shift+home", tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift}, []byte("\x1b[1;2H")},
		{"alt+left", tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}, []byte("\x1b[1;3D")},
		{"ctrl+shift+end", tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl | tea.ModShift}, []byte("\x1b[1;6F")},
		{"ctrl+delete", tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModCtrl}, []byte("\x1b[3;5~")},
		{"ctrl+insert", tea.KeyPressMsg{Code: tea.KeyInsert, Mod: tea.ModCtrl}, []byte("\x1b[2;5~")},
		{"ctrl+pgup", tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl}, []byte("\x1b[5;5~")},
		{"ctrl+pgdown", tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModCtrl}, []byte("\x1b[6;5~")},
		// All three modifiers together → param 8 (1 + shift(1) + alt(2) + ctrl(4)).
		{"ctrl+shift+alt+end", tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl | tea.ModShift | tea.ModAlt}, []byte("\x1b[1;8F")},
		{"bare home stays CSI H", tea.KeyPressMsg{Code: tea.KeyHome}, []byte("\x1b[H")},
		{"bare end stays CSI F", tea.KeyPressMsg{Code: tea.KeyEnd}, []byte("\x1b[F")},
		// shift+up is a modified cursor key now, not an unencoded key.
		{"shift+up", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}, []byte("\x1b[1;2A")},
		// A modifier on a non-nav key isn't caught by modifiedNavSeq; ctrl+a still
		// falls through to the ctrl-chord branch.
		{"ctrl+a falls through to chord", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, []byte{0x01}},
	}
	for _, tc := range cases {
		got := ptyBytesFor(tc.msg)
		if string(got) != string(tc.want) {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// TestSendTermKeyLoopPreservesOrder verifies the single ordered sender delivers
// keystrokes in arrival order; racing per-key commands could reorder bytes.
func TestSendTermKeyLoopPreservesOrder(t *testing.T) {
	c := &recordingClient{}
	ch := make(chan termKey, 64)
	done := make(chan struct{})
	go func() { sendTermKeyLoop(c, ch); close(done) }()

	const seq = "abcdefghijklmnopqrstuvwxyz0123456789"
	for _, r := range seq {
		ch <- termKey{termID: "t1", data: []byte{byte(r)}}
	}
	close(ch)
	<-done

	if got := c.terminalInputData(t); got != seq {
		t.Errorf("ordered terminal.input = %q, want %q", got, seq)
	}
}

// TestHandleScreenKeyEnqueuesInOrder verifies keystrokes are enqueued on the
// ordered channel rather than each returning a racing command.
func TestHandleScreenKeyEnqueuesInOrder(t *testing.T) {
	m, _ := screenModel()
	for _, r := range []rune{'a', 'b', 'c'} {
		res, cmd := m.handleScreenKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
		if cmd != nil {
			t.Fatal("expected no per-key cmd: input ordering must go through the channel")
		}
	}
	got := ""
	for i := 0; i < 3; i++ {
		select {
		case k := <-m.termKeyCh:
			got += string(k.data)
		default:
			t.Fatalf("missing queued key #%d", i)
		}
	}
	if got != "abc" {
		t.Errorf("queued order = %q, want abc", got)
	}
}

func TestWindowResizeWhileAttached(t *testing.T) {
	c := &recordingClient{}
	m := testModel()
	m.client = c
	m.mode = modeScreen
	m.termID = "s1"
	m.term = vt.NewEmulator(80, 24)

	res, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = res.(model)
	if m.term.Width() != max(10, 100-2) {
		t.Errorf("emulator width=%d want %d", m.term.Width(), max(10, 100-2))
	}
	runCmd(cmd)
	if !slices.Contains(c.calledMethods(), api.MethodTerminalResize) {
		t.Errorf("resize: calls=%v want terminal.resize", c.calledMethods())
	}
}

func TestDisconnectLeavesScreen(t *testing.T) {
	m := testModel()
	m.mode = modeScreen
	m.screenReturn = modeList
	m.termID = "s1"
	m.term = vt.NewEmulator(80, 24)

	res, _ := m.Update(connStateMsg{connected: false})
	m = res.(model)
	if m.mode != modeList {
		t.Errorf("disconnect: mode=%v want list", m.mode)
	}
	if m.term != nil || m.termID != "" {
		t.Errorf("disconnect: term not cleared (term=%v id=%q)", m.term, m.termID)
	}
}

func TestApplyEventWritesTerminalOutput(t *testing.T) {
	m := testModel()
	m.termID = "s1"
	m.term = vt.NewEmulator(80, 24)

	data := base64.StdEncoding.EncodeToString([]byte("hello"))
	params, _ := json.Marshal(api.TerminalOutput{TermID: "s1", Data: data})
	m.applyEvent(api.Notification{Method: api.MethodTerminalOutput, Params: params})

	if !strings.Contains(m.term.Render(), "hello") {
		t.Errorf("render=%q want to contain hello", m.term.Render())
	}

	// Output for a different term id is ignored.
	other, _ := json.Marshal(api.TerminalOutput{TermID: "other", Data: base64.StdEncoding.EncodeToString([]byte("zzz"))})
	m.applyEvent(api.Notification{Method: api.MethodTerminalOutput, Params: other})
	if strings.Contains(m.term.Render(), "zzz") {
		t.Errorf("render=%q must not contain output for another term", m.term.Render())
	}
}
