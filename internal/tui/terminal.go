package tui

import (
	"encoding/base64"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/charmbracelet/x/vt"
)

// ptyBytesFor encodes a key press to the raw bytes a PTY expects.
// Returns nil for keys with no encoding.
func ptyBytesFor(msg tea.KeyPressMsg) []byte {
	// Alt+Enter / Shift+Enter → ESC+CR (meta-Enter): "insert newline" in agent TUIs.
	// Matched on Code+Mod because msg.String() is unreliable here.
	if msg.Code == tea.KeyEnter && msg.Mod&(tea.ModAlt|tea.ModShift) != 0 {
		return []byte("\x1b\r")
	}
	// Modified cursor/nav keys (Ctrl/Shift/Alt+Home/End/arrows/PgUp…). Matched on
	// Code+Mod before the string switch, which only sees the bare keys.
	if b := modifiedNavSeq(msg); b != nil {
		return b
	}
	s := msg.String()
	switch s {
	case "enter":
		return []byte{'\r'}
	case "tab":
		return []byte{'\t'}
	case "shift+tab":
		return []byte("\x1b[Z")
	case "backspace":
		return []byte{0x7f}
	case "esc", "escape":
		return []byte{0x1b}
	case "delete":
		return []byte("\x1b[3~")
	case "insert":
		return []byte("\x1b[2~")
	case "up":
		return []byte("\x1b[A")
	case "down":
		return []byte("\x1b[B")
	case "right":
		return []byte("\x1b[C")
	case "left":
		return []byte("\x1b[D")
	case "home":
		return []byte("\x1b[H")
	case "end":
		return []byte("\x1b[F")
	case "pgup":
		return []byte("\x1b[5~")
	case "pgdown":
		return []byte("\x1b[6~")
	case "space":
		return []byte{' '}
	}
	// Ctrl chords: ctrl+a..z → 0x01..0x1a.
	if rest, ok := strings.CutPrefix(s, "ctrl+"); ok && len(rest) == 1 {
		if c := rest[0]; c >= 'a' && c <= 'z' {
			return []byte{c - 'a' + 1}
		}
	}
	// Alt chords: alt+x → ESC x.
	if rest, ok := strings.CutPrefix(s, "alt+"); ok && len(rest) == 1 {
		return []byte{0x1b, rest[0]}
	}
	// Function keys f1..f12.
	if len(s) >= 2 && s[0] == 'f' {
		if n, err := strconv.Atoi(s[1:]); err == nil {
			if seq, ok := fnKeySeqs[n]; ok {
				return []byte(seq)
			}
		}
	}
	if msg.Text != "" {
		return []byte(msg.Text)
	}
	return nil
}

var fnKeySeqs = map[int]string{
	1: "\x1bOP", 2: "\x1bOQ", 3: "\x1bOR", 4: "\x1bOS",
	5: "\x1b[15~", 6: "\x1b[17~", 7: "\x1b[18~", 8: "\x1b[19~",
	9: "\x1b[20~", 10: "\x1b[21~", 11: "\x1b[23~", 12: "\x1b[24~",
}

// Cursor keys encode a held modifier as CSI 1;<param><final>; the ~-style nav
// keys as CSI <num>;<param>~ (xterm). param = 1 + shift(1) + alt(2) + ctrl(4).
var cursorFinals = map[rune]byte{
	tea.KeyUp: 'A', tea.KeyDown: 'B', tea.KeyRight: 'C', tea.KeyLeft: 'D',
	tea.KeyHome: 'H', tea.KeyEnd: 'F',
}

var tildeNums = map[rune]int{
	tea.KeyInsert: 2, tea.KeyDelete: 3, tea.KeyPgUp: 5, tea.KeyPgDown: 6,
}

// modifiedNavSeq returns the xterm sequence for a cursor/nav key pressed with a
// Ctrl/Shift/Alt modifier, or nil if msg isn't such a key or holds no relevant
// modifier (bare keys fall through to the plain-key encoding). This form is what
// programs expect for Ctrl+Home/End etc.; it applies regardless of DECCKM.
func modifiedNavSeq(msg tea.KeyPressMsg) []byte {
	mod := 0
	if msg.Mod&tea.ModShift != 0 {
		mod |= 1
	}
	if msg.Mod&tea.ModAlt != 0 {
		mod |= 2
	}
	if msg.Mod&tea.ModCtrl != 0 {
		mod |= 4
	}
	if mod == 0 {
		return nil
	}
	param := strconv.Itoa(mod + 1)
	if final, ok := cursorFinals[msg.Code]; ok {
		return []byte("\x1b[1;" + param + string(final))
	}
	if num, ok := tildeNums[msg.Code]; ok {
		return []byte("\x1b[" + strconv.Itoa(num) + ";" + param + "~")
	}
	return nil
}

// termOpenedMsg reports the result of a terminal.open request.
type termOpenedMsg struct {
	termID string
	err    error
}

// termDims maps the screen box geometry to the attach's cols/rows.
func (m model) termDims() (cols, rows int) {
	return max(10, m.width-2), max(1, m.height-6)
}

// enterScreen opens a live attach for id and switches to the screen view. It must
// be called while m.mode still holds the origin mode (captured into screenReturn).
func (m model) enterScreen(id string) (model, tea.Cmd) {
	cols, rows := m.termDims()
	termID := newTermID()
	m.selectedID = id
	m.screenReturn = m.mode
	m.mode = modeScreen
	m.termID = termID
	m.termErr = nil
	m.term = vt.NewEmulator(cols, rows)
	m.termStop = make(chan struct{})
	go drainEmulator(m.term, m.termStop)
	return m, m.termOpenCmd(id, termID, cols, rows)
}

// drainEmulator discards the emulator's auto-generated query replies (DA/DSR/
// in-band resize); undrained, it deadlocks a Write on the first query. Also owns
// Close (vt's closed flag isn't goroutine-safe), so detachScreen pokes InputPipe.
func drainEmulator(e *vt.Emulator, stop <-chan struct{}) {
	buf := make([]byte, 4096)
	for {
		if _, err := e.Read(buf); err != nil {
			return
		}
		select {
		case <-stop:
			_ = e.Close()
			return
		default:
		}
	}
}

// detachScreen resets local attach state and stops the drain goroutine. It does
// not touch the node.
func (m model) detachScreen() model {
	if m.termStop != nil {
		close(m.termStop)
		_, _ = m.term.InputPipe().Write([]byte{0}) // wake drainEmulator's Read so it observes stop
	}
	m.mode = m.screenReturn
	m.term, m.termID, m.termStop, m.termErr = nil, "", nil, nil
	return m
}

// leaveScreen tears down the attach and closes the terminal on the node.
func (m model) leaveScreen() (model, tea.Cmd) {
	termID := m.termID
	return m.detachScreen(), m.termCloseCmd(termID)
}

func (m model) termOpenCmd(id, termID string, cols, rows int) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		err := client.Call(api.MethodTerminalOpen, api.TerminalOpenParams{
			TermID: termID, SessionID: id, Cols: cols, Rows: rows,
		}, nil)
		return termOpenedMsg{termID: termID, err: err}
	}
}

func (m model) termCloseCmd(termID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodTerminalClose, api.TerminalCloseParams{TermID: termID}, nil)
		return nil
	}
}

// termKey is one chunk of terminal input (a keystroke's bytes or a paste) tagged
// with its attach id, queued for the single ordered sender.
type termKey struct {
	termID string
	data   []byte
}

// termKeyBuf is the ordered-input queue depth: larger than any human burst, so a
// full queue means the node is wedged and an excess key is dropped, not blocked.
const termKeyBuf = 512

// sendTermKey queues a keystroke for the ordered sender. Called from the serial
// Update loop, so enqueue order is arrival order. Non-blocking: never stalls the
// UI thread (drops on a full queue).
func (m model) sendTermKey(termID string, data []byte) {
	if m.termKeyCh == nil || len(data) == 0 {
		return
	}
	select {
	case m.termKeyCh <- termKey{termID: termID, data: data}:
	default: // queue full; drop rather than block the UI
	}
}

// sendTermKeyLoop forwards queued keystrokes to the node in arrival order,
// coalescing consecutive keys for one attach into a single terminal.input. One
// goroutine guarantees ordering; concurrent per-key commands could reorder bytes.
func sendTermKeyLoop(client Client, ch <-chan termKey) {
	for k := range ch {
		termID := k.termID
		data := append([]byte(nil), k.data...)
		for draining := true; draining; {
			select {
			case n, ok := <-ch:
				if !ok {
					flushTermInput(client, termID, data)
					return
				}
				if n.termID != termID {
					flushTermInput(client, termID, data)
					termID, data = n.termID, append([]byte(nil), n.data...)
				} else {
					data = append(data, n.data...)
				}
			default:
				draining = false
			}
		}
		flushTermInput(client, termID, data)
	}
}

func flushTermInput(client Client, termID string, data []byte) {
	if len(data) == 0 {
		return
	}
	enc := base64.StdEncoding.EncodeToString(data)
	_ = client.Call(api.MethodTerminalInput, api.TerminalInputParams{TermID: termID, Data: enc}, nil)
}

func (m model) termResizeCmd(termID string, cols, rows int) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodTerminalResize, api.TerminalResizeParams{TermID: termID, Cols: cols, Rows: rows}, nil)
		return nil
	}
}
