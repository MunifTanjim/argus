package tui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var rexKeyNames = map[string]string{
	"enter": "enter", "escape": "esc", "tab": "tab", "space": "space",
	"backspace": "backspace", "delete": "delete",
	"left": "left", "right": "right", "up": "up", "down": "down",
	"home": "home", "end": "end", "pageup": "pgup", "pagedown": "pgdown",
}

// parseRexKeySeq parses a key sequence in Rex notation, for example "ctrl+h"
// or "ctrl+a>c". Unlike Rex, a character is the character the terminal sends,
// so "&" and "D" are keys, and "shift+7" is not "&".
func parseRexKeySeq(s string) (keySeq, error) {
	var seq keySeq
	steps := splitRexSeq(s)
	for _, k := range steps {
		st, err := parseRexKey(k)
		if err != nil {
			if len(steps) > 1 {
				err = fmt.Errorf("%q: %w", s, err)
			}
			return nil, err
		}
		seq = append(seq, st)
	}
	return seq, nil
}

// splitRexSeq splits at each ">" that ends a key. A ">" at the start of a key
// or after a "+" is the key itself, as in "ctrl+a>>" or "alt+>".
func splitRexSeq(s string) []string {
	var steps []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '>' || i == start || s[i-1] == '+' {
			continue
		}
		steps = append(steps, s[start:i])
		start = i + 1
	}
	return append(steps, s[start:])
}

func parseRexKey(s string) (keyStep, error) {
	k := s
	var modToks []string
	switch i := strings.LastIndex(s, "+"); {
	case s == "+":
	case strings.HasSuffix(s, "++"):
		modToks, k = strings.Split(s[:len(s)-2], "+"), "+"
	case i >= 0:
		modToks, k = strings.Split(s[:i], "+"), s[i+1:]
	}
	var ctrl, alt, shift, super bool
	for _, t := range modToks {
		switch strings.ToLower(t) {
		case "ctrl", "control":
			ctrl = true
		case "alt", "opt", "option":
			alt = true
		case "shift":
			shift = true
		case "cmd", "command", "super", "meta", "win":
			super = true
		default:
			return nil, fmt.Errorf("unknown key %q", s)
		}
	}
	name := strings.ToLower(k)
	mods := modPrefix(ctrl, alt, shift, super)
	if n, ok := rexKeyNames[name]; ok {
		return keyStep{mods + n}, nil
	}
	if isFnKey(name) {
		return keyStep{mods + name}, nil
	}
	if len(k) > 2 && k[0] == '[' && k[len(k)-1] == ']' {
		return nil, fmt.Errorf("%q: physical keys are not supported", s)
	}
	r, n := utf8.DecodeRuneInString(k)
	if n == 0 || n != len(k) || unicode.IsSpace(r) {
		return nil, fmt.Errorf("unknown key %q", s)
	}
	return charStep(r, ctrl, alt, shift, super), nil
}
