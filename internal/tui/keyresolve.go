package tui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type keyTimeoutMsg struct{ gen int }

// keysRaw reports whether keys skip the keymaps and go to the handlers as typed.
func (m model) keysRaw() bool {
	return m.topScreen() >= 0 || len(m.popups) > 0 || m.focusedComp().raw(&ctx{m: &m}) || m.showHelp
}

// resolveKey follows Vim: a key that is both a mapping and the start of a
// longer one waits key-timeout for the next key.
func (m model) resolveKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" && len(m.keyBuf) > 0 {
		m.clearKeys()
		return m, nil
	}
	matched, toFeed, cmd := m.feedKey(msg)
	return m.runSequence(matched, toFeed, cmd)
}

func (m model) keyTimeout(msg keyTimeoutMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.keyGen || len(m.keyBuf) == 0 {
		return m, nil
	}
	if m.keysRaw() {
		m.clearKeys()
		return m, nil
	}
	matched, toFeed, cmd := m.flushBuf(false)
	return m.runSequence(matched, toFeed, cmd)
}

// keyPress is a completed match. fallback is what the handlers see when a Lua
// function does not handle msg.
type keyPress struct{ msg, fallback tea.KeyPressMsg }

func pressOf(k tea.KeyPressMsg) keyPress { return keyPress{k, k} }

func (m *model) feedKey(msg tea.KeyPressMsg) (matched []keyPress, toFeed []tea.KeyPressMsg, cmd tea.Cmd) {
	m.keyBuf = append(m.keyBuf, msg)
	km := m.keymap()
	exact, longer := km.lookup(m.screen(), m.keyBuf)
	switch {
	case exact != "" && !longer:
		k := m.matchOf(m.keyBuf, exact)
		m.clearKeys()
		return []keyPress{k}, nil, nil
	case longer:
		if exact != "" {
			m.keyMatch, m.keyMatchN = exact, len(m.keyBuf)
		}
		m.keyGen++
		gen := m.keyGen
		return nil, nil, tea.Tick(km.timeout, func(time.Time) tea.Msg { return keyTimeoutMsg{gen} })
	}
	return m.flushBuf(true)
}

// flushBuf ends the pending sequence. mismatch is false on a timeout, which
// drops unmatched keys.
func (m *model) flushBuf(mismatch bool) (matched []keyPress, toFeed []tea.KeyPressMsg, cmd tea.Cmd) {
	buf, match, n := m.keyBuf, m.keyMatch, m.keyMatchN
	m.clearKeys()
	switch {
	case match != "":
		return []keyPress{m.matchOf(buf[:n], match)}, buf[n:], nil
	case !mismatch:
		return nil, nil, nil
	case len(buf) == 1:
		return []keyPress{pressOf(buf[0])}, nil, nil
	default:
		return nil, buf[len(buf)-1:], nil
	}
}

func (m *model) clearKeys() {
	m.keyBuf, m.keyMatch, m.keyMatchN = nil, "", 0
}

// runSequence checks keysRaw after each key so that keys following a match
// that opens a text input go to the input as typed, not through the resolver.
func (m model) runSequence(matched []keyPress, toFeed []tea.KeyPressMsg, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{cmd}
	run := func(k keyPress) {
		mm, c, ok := m.runLua(k.msg)
		if !ok {
			res, kc := mm.runKey(k.fallback)
			mm = res.(model)
			c = tea.Batch(c, kc)
		}
		m = mm.repairFocus()
		cmds = append(cmds, c)
	}
	for _, k := range matched {
		run(k)
	}
	for len(toFeed) > 0 {
		next := toFeed[0]
		toFeed = toFeed[1:]
		if m.opensCmdLine(next) {
			mm, c := m.openCmdLine()
			m = mm.(model)
			cmds = append(cmds, c)
			continue
		}
		if m.keysRaw() {
			run(pressOf(next))
			continue
		}
		newMatched, newToFeed, timerCmd := m.feedKey(next)
		cmds = append(cmds, timerCmd)
		for _, k := range newMatched {
			run(k)
		}
		toFeed = append(newToFeed, toFeed...)
	}
	return m, tea.Batch(cmds...)
}

// matchOf is the match of keys with id. A Lua function binding leaves the
// keys to the other mappings, which can match them in another form.
func (m *model) matchOf(keys []tea.KeyPressMsg, id string) keyPress {
	k := seqMsg(keys, id)
	sk := m.keymap().screenKeys(m.screen())
	if len(keys) == 1 || sk.lua[id] == nil {
		return pressOf(k)
	}
	if _, base, _ := resolveSeqs(sk.base, keys, keyTexts(keys)); base != "" {
		return keyPress{k, seqMsg(keys, base)}
	}
	return pressOf(k)
}

// seqMsg is the key press the handlers see for a completed match: the press
// itself for one key, or a synthetic press whose text is the sequence id.
func seqMsg(keys []tea.KeyPressMsg, id string) tea.KeyPressMsg {
	if len(keys) == 1 {
		return keys[0]
	}
	return tea.KeyPressMsg{Code: tea.KeyExtended, Text: id}
}

func (km *keymap) lookup(screen string, buf []tea.KeyPressMsg) (exact string, longer bool) {
	_, exact, longer = km.screenKeys(screen).resolve(buf, keyTexts(buf))
	return exact, longer
}

// resolve matches pressed, the texts of buf, or else buf without the
// modifiers that its first key holds, as Vim reads <C-w><C-h> as <C-w>h. keys
// is the form that matched.
func (sk *screenKeys) resolve(buf []tea.KeyPressMsg, pressed []string) (keys []string, exact string, longer bool) {
	return resolveSeqs(sk.seqs, buf, pressed)
}

func resolveSeqs(seqs []screenSeq, buf []tea.KeyPressMsg, pressed []string) (keys []string, exact string, longer bool) {
	exact, longer = matchSeqs(seqs, pressed)
	if exact == "" && !longer {
		if held := withoutHeldMods(buf, pressed); held != nil {
			if e, l := matchSeqs(seqs, held); e != "" || l {
				return held, e, l
			}
		}
	}
	return pressed, exact, longer
}

func matchSeqs(seqs []screenSeq, pressed []string) (exact string, longer bool) {
	for _, s := range seqs {
		switch {
		case len(s.seq) < len(pressed) || !s.seq.startsWith(pressed):
		case len(s.seq) > len(pressed):
			longer = true
		case exact == "":
			exact = s.id
		}
	}
	return exact, longer
}

// withoutHeldMods drops from each key after the first the modifiers that the
// first key has, shift aside because it changes the character. It is nil when
// nothing changes.
func withoutHeldMods(buf []tea.KeyPressMsg, pressed []string) []string {
	if len(buf) < 2 {
		return nil
	}
	held := buf[0].Mod & (tea.ModCtrl | tea.ModAlt | tea.ModMeta | tea.ModHyper | tea.ModSuper)
	if held == 0 {
		return nil
	}
	var out []string
	for i, k := range buf[1:] {
		if k.Mod&held == 0 {
			continue
		}
		k.Mod &^= held
		if out == nil {
			out = slices.Clone(pressed)
		}
		out[i+1] = k.String()
	}
	return out
}

// keyTexts exists because String() allocates: a lookup computes it once per
// key instead of once per candidate.
func keyTexts(buf []tea.KeyPressMsg) []string {
	out := make([]string, len(buf))
	for i, k := range buf {
		out[i] = k.String()
	}
	return out
}

func (s keySeq) startsWith(pressed []string) bool {
	for i, k := range pressed {
		if !slices.Contains(s[i], k) {
			return false
		}
	}
	return true
}

// keyHint is the footer while a sequence waits.
func (m model) keyHint() string {
	pressed := keyTexts(m.keyBuf)
	var typed strings.Builder
	for _, k := range pressed {
		typed.WriteString(keyLabel(k))
	}
	parts := []string{typed.String() + "…"}
	// Each next key shows once: what it completes when it completes a
	// mapping, else the first mapping it starts.
	var order []string
	labels := map[string]string{}
	completes := map[string]bool{}
	var wait string
	sk := m.keymap().screenKeys(m.screen())
	pressed, _, _ = sk.resolve(m.keyBuf, pressed)
	for _, s := range sk.seqs {
		if wait == "" && m.keyMatch != "" && s.id == m.keyMatch {
			wait = s.label()
		}
		if len(s.seq) <= len(pressed) || !s.seq.startsWith(pressed) {
			continue
		}
		next := keyLabel(s.seq[len(pressed)][0])
		done := len(s.seq) == len(pressed)+1
		if _, ok := labels[next]; !ok {
			order = append(order, next)
		} else if completes[next] || !done {
			continue
		}
		labels[next], completes[next] = s.label(), done
	}
	for _, next := range order {
		parts = append(parts, next+" "+labels[next])
	}
	if wait != "" {
		parts = append(parts, "(wait) "+wait)
	}
	return strings.Join(parts, " · ")
}

// label names s in the footer: its desc, else its command.
func (s screenSeq) label() string {
	if s.desc != "" {
		return s.desc
	}
	return s.cmd
}
