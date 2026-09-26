package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// keyStep is one key press of a keymap entry: the tea.KeyPressMsg.String()
// values the press can arrive as. The first is canonical.
type keyStep []string

type keySeq []keyStep

// seqMark starts the text of a synthetic key press that carries a multi-key
// sequence. It is a private-use rune, so no real key press has it.
const seqMark = "\uE000"

// leaderToken marks a <Leader> step until the keymap replaces it with the leader.
const leaderToken = "leader"

func (s keySeq) id() string {
	if len(s) == 1 {
		return s[0][0]
	}
	parts := make([]string, len(s))
	for i, st := range s {
		parts[i] = st[0]
	}
	return seqMark + strings.Join(parts, " ")
}

var keyNames = map[string]string{
	"cr": "enter", "enter": "enter", "return": "enter",
	"esc": "esc", "space": "space", "tab": "tab", "bs": "backspace",
	"up": "up", "down": "down", "left": "left", "right": "right",
	"pageup": "pgup", "pagedown": "pgdown", "home": "home", "end": "end",
	"lt": "<",
}

// parseKeySeq parses a key sequence in Vim notation, for example "g.",
// "<C-b>", or "g<S-Tab>".
func parseKeySeq(s string) (keySeq, error) {
	if s == "" {
		return nil, errors.New("empty key")
	}
	var seq keySeq
	for s != "" {
		if s[0] == '<' {
			end := strings.IndexByte(s, '>')
			if end < 0 {
				return nil, fmt.Errorf("no closing > (write < as <lt>)")
			}
			st, err := parseSpecial(s[1:end])
			if err != nil {
				return nil, err
			}
			seq = append(seq, st)
			s = s[end+1:]
			continue
		}
		r, n := utf8.DecodeRuneInString(s)
		seq = append(seq, charStep(r, false, false, false, false))
		s = s[n:]
	}
	return seq, nil
}

func parseSpecial(body string) (keyStep, error) {
	toks := strings.Split(body, "-")
	if len(toks) > 1 && toks[len(toks)-1] == "" {
		toks = append(toks[:len(toks)-2], "-")
	}
	var ctrl, alt, shift, super bool
	for _, t := range toks[:len(toks)-1] {
		switch strings.ToLower(t) {
		case "c":
			ctrl = true
		case "m", "a":
			alt = true
		case "s":
			shift = true
		case "d":
			super = true
		default:
			return nil, fmt.Errorf("unknown modifier %q in <%s>", t, body)
		}
	}
	k := toks[len(toks)-1]
	mods := ctrl || alt || shift || super
	if strings.ToLower(k) == "leader" && !mods {
		return keyStep{leaderToken}, nil
	}
	if name, ok := keyNames[strings.ToLower(k)]; ok {
		return keyStep{modPrefix(ctrl, alt, shift, super) + name}, nil
	}
	if f, ok := strings.CutPrefix(strings.ToLower(k), "f"); ok {
		if n, err := strconv.Atoi(f); err == nil && n >= 1 && n <= 12 {
			return keyStep{modPrefix(ctrl, alt, shift, super) + "f" + f}, nil
		}
	}
	if r, n := utf8.DecodeRuneInString(k); mods && n == len(k) && n > 0 {
		return charStep(r, ctrl, alt, shift, super), nil
	}
	return nil, fmt.Errorf("unknown key <%s>", body)
}

// ctrl ignores the letter case. A shifted letter arrives as its capital or as
// "shift+" and the letter.
func charStep(r rune, ctrl, alt, shift, super bool) keyStep {
	if r == ' ' && !ctrl && !alt && !shift && !super {
		return keyStep{"space"}
	}
	lower := unicode.ToLower(r)
	switch {
	case ctrl:
		return keyStep{modPrefix(true, alt, shift, super) + string(lower)}
	case unicode.IsLetter(r) && (shift || unicode.IsUpper(r)):
		upper := string(unicode.ToUpper(r))
		return keyStep{modPrefix(false, alt, false, super) + upper, modPrefix(false, alt, true, super) + string(lower)}
	}
	return keyStep{modPrefix(false, alt, shift, super) + string(r)}
}

// modPrefix writes modifiers in bubbletea's keystroke order.
func modPrefix(ctrl, alt, shift, super bool) string {
	var b strings.Builder
	for _, m := range []struct {
		on   bool
		name string
	}{{ctrl, "ctrl+"}, {alt, "alt+"}, {shift, "shift+"}, {super, "super+"}} {
		if m.on {
			b.WriteString(m.name)
		}
	}
	return b.String()
}
