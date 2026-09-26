package tui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type keyTimeoutMsg struct{ gen int }

// keysRaw reports whether keys go to the handlers as typed: text inputs, y/n
// confirmations, the open help, and the live screen have no keymaps.
func (m model) keysRaw() bool {
	return m.mode == modeScreen || m.typing() || m.pendingKill || m.spawn.active() ||
		m.inputActive() || m.projects.pendingRemove != "" || m.projects.pendingForget != "" ||
		m.projects.pendingKill != "" || m.projects.offerSpawn != nil || m.projects.create.active ||
		m.projects.retarget != nil || m.projects.showHelp || m.pendingExport || m.redact.pendingSave
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

func (m *model) feedKey(msg tea.KeyPressMsg) (matched []tea.KeyPressMsg, toFeed []tea.KeyPressMsg, cmd tea.Cmd) {
	m.keyBuf = append(m.keyBuf, msg)
	km := m.keymap()
	exact, longer := km.lookup(m.screen(), m.keyBuf)
	switch {
	case exact != "" && !longer:
		k := seqMsg(m.keyBuf, exact)
		m.clearKeys()
		return []tea.KeyPressMsg{k}, nil, nil
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
func (m *model) flushBuf(mismatch bool) (matched []tea.KeyPressMsg, toFeed []tea.KeyPressMsg, cmd tea.Cmd) {
	buf, match, n := m.keyBuf, m.keyMatch, m.keyMatchN
	m.clearKeys()
	switch {
	case match != "":
		return []tea.KeyPressMsg{seqMsg(buf[:n], match)}, buf[n:], nil
	case !mismatch:
		return nil, nil, nil
	case len(buf) == 1:
		return buf, nil, nil
	default:
		return nil, buf[len(buf)-1:], nil
	}
}

func (m *model) clearKeys() {
	m.keyBuf, m.keyMatch, m.keyMatchN = nil, "", 0
}

// runSequence checks keysRaw after each key so that keys following a match
// that opens a text input go to the input as typed, not through the resolver.
func (m model) runSequence(matched []tea.KeyPressMsg, toFeed []tea.KeyPressMsg, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{cmd}
	for _, k := range matched {
		mm, c := m.runKey(k)
		m = mm.(model)
		cmds = append(cmds, c)
	}
	for len(toFeed) > 0 {
		next := toFeed[0]
		toFeed = toFeed[1:]
		if m.keysRaw() {
			mm, c := m.runKey(next)
			m = mm.(model)
			cmds = append(cmds, c)
			continue
		}
		newMatched, newToFeed, timerCmd := m.feedKey(next)
		cmds = append(cmds, timerCmd)
		for _, k := range newMatched {
			mm, c := m.runKey(k)
			m = mm.(model)
			cmds = append(cmds, c)
		}
		toFeed = append(newToFeed, toFeed...)
	}
	return m, tea.Batch(cmds...)
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
	sk := km.screenKeys(screen)
	pressed := keyTexts(buf)
	for _, s := range sk.seqs {
		if len(s.seq) < len(pressed) || !s.seq.startsWith(pressed) {
			continue
		}
		if len(s.seq) == len(pressed) {
			exact = s.id
		} else {
			longer = true
		}
	}
	return exact, longer
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
	seen := map[string]bool{}
	sk := m.keymap().screenKeys(m.screen())
	for _, s := range sk.seqs {
		if len(s.seq) <= len(pressed) || !s.seq.startsWith(pressed) {
			continue
		}
		next := keyLabel(s.seq[len(pressed)][0])
		if !seen[next] {
			seen[next] = true
			parts = append(parts, next+" "+s.cmd)
		}
	}
	if m.keyMatch != "" {
		for _, s := range sk.seqs {
			if s.id == m.keyMatch {
				parts = append(parts, "(wait) "+s.cmd)
				break
			}
		}
	}
	return strings.Join(parts, " · ")
}
