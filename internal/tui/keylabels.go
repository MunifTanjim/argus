package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
)

// labelPairs names the two commands whose keys share one footer label, in
// label order ("gg/G" is goto top then goto bottom).
var labelPairs = func() map[string][2]string {
	out := map[string][2]string{}
	for _, p := range [][2]string{
		{"prev", "next"}, {"scroll up", "scroll down"}, {"goto top", "goto bottom"},
		{"scroll half-page-up", "scroll half-page-down"}, {"tab prev", "tab next"},
		{"fold close", "fold open"}, {"sidebar narrower", "sidebar wider"}, {"next diff-file", "prev diff-file"},
		{"next card", "prev card"},
		{"focus next", "focus prev"},
	} {
		out[p[0]], out[p[1]] = p, p
	}
	return out
}()

// helpBinding is b as the footer shows it on the current screen.
func (m model) helpBinding(b binding) key.Binding {
	h := b.Help()
	if b.name == "" {
		sk := m.keymap().screenKeys(m.screen())
		if keys := sk.keys(b); len(keys) > 0 {
			out := b.Binding
			out.SetHelp(keyLabel(keys[0]), h.Desc)
			return out
		}
		return b.Binding
	}
	if h.Desc == "" {
		return b.Binding
	}
	sk := m.keymap().screenKeys(m.screen())
	names := []string{b.name}
	if p, ok := labelPairs[b.name]; ok {
		names = p[:]
	}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if k := m.firstKey(sk, b, n); k != "" {
			parts = append(parts, keyLabel(k))
		}
	}
	if len(parts) == 0 {
		return b.Binding
	}
	out := b.Binding
	out.SetHelp(strings.Join(parts, "/"), h.Desc)
	return out
}

// hintText is the footer text of bs with their effective keys, for footers
// that render one plain line.
func (m model) hintText(bs ...binding) string {
	parts := make([]string, len(bs))
	for i, b := range bs {
		h := m.helpBinding(b).Help()
		parts[i] = h.Key + " " + h.Desc
	}
	return strings.Join(parts, " · ")
}

func (m model) firstKey(sk *screenKeys, b binding, name string) string {
	if keys := cmdKeys(sk, m.screen(), b, name); len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// When name matches b itself, b's effective keys are used; otherwise the screen
// bindings are searched for name.
func cmdKeys(sk *screenKeys, screen string, b binding, name string) []string {
	if b.name == name {
		return sk.keys(b)
	}
	for _, sb := range screenBindings[screen] {
		if sb.name == name {
			return sk.keys(sb)
		}
	}
	return nil
}

// helpKeys returns the key column text for the help table: for each command in
// b's pair (or b alone), all effective keys joined by a space; the commands
// joined by "/". Keys are always resolved against the projects screen so that
// pair partners are found even when the help is shown from another mode.
func (m model) helpKeys(b binding) string {
	sk := m.keymap().screenKeys("projects")
	names := []string{b.name}
	if p, ok := labelPairs[b.name]; ok {
		names = p[:]
	}
	cmds := make([]string, 0, len(names))
	for _, n := range names {
		ids := cmdKeys(sk, "projects", b, n)
		if len(ids) == 0 {
			continue
		}
		labels := make([]string, len(ids))
		for i, id := range ids {
			labels[i] = keyLabel(id)
		}
		cmds = append(cmds, strings.Join(labels, " "))
	}
	return strings.Join(cmds, "/")
}

// keyText is the label of b's first effective key, for sentences that name a
// key.
func (m model) keyText(b binding) string { return m.keyTextOn(m.screen(), b) }

func (m model) keyTextOn(screen string, b binding) string {
	if keys := m.keymap().screenKeys(screen).keys(b); len(keys) > 0 {
		return keyLabel(keys[0])
	}
	return b.name
}

// keyLabel shows a key id the way footers write keys: ^ for ctrl, arrows as
// glyphs, space as ␣, and a sequence as its keys run together.
func keyLabel(id string) string {
	if rest, ok := strings.CutPrefix(id, seqMark); ok {
		var b strings.Builder
		for _, k := range strings.Split(rest, " ") {
			b.WriteString(keyLabel(k))
		}
		return b.String()
	}
	switch id {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "space":
		return "␣"
	}
	if k, ok := strings.CutPrefix(id, "ctrl+"); ok {
		return "^" + k
	}
	return id
}
