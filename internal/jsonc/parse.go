package jsonc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func toAny(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func renderJSON(v any, prefix, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// jnode is a JSON-with-comments value with its byte span in the source.
type jnode struct {
	kind       byte // '{', '[', or 0 for a scalar
	start, end int
	items      []jitem
}

type jitem struct {
	key   string // empty for array elements
	start int    // where the key, or the element, begins
	val   *jnode
}

func (n *jnode) member(key string) *jitem {
	if n.kind != '{' {
		return nil
	}
	for i := len(n.items) - 1; i >= 0; i-- {
		if n.items[i].key == key {
			return &n.items[i]
		}
	}
	return nil
}

func (n *jnode) equal(src []byte, v any) bool {
	got, err := n.decode(src)
	return err == nil && reflect.DeepEqual(got, toAny(v))
}

func (n *jnode) index(src []byte, v any) int {
	for i, it := range n.items {
		if it.val.equal(src, v) {
			return i
		}
	}
	return -1
}

func (n *jnode) decode(src []byte) (any, error) {
	switch n.kind {
	case '{':
		m := make(map[string]any, len(n.items))
		for _, it := range n.items {
			v, err := it.val.decode(src)
			if err != nil {
				return nil, err
			}
			m[it.key] = v
		}
		return m, nil
	case '[':
		a := make([]any, 0, len(n.items))
		for _, it := range n.items {
			v, err := it.val.decode(src)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		return a, nil
	}
	var v any
	if err := json.Unmarshal(src[n.start:n.end], &v); err != nil {
		return nil, fmt.Errorf("invalid value at offset %d", n.start)
	}
	return v, nil
}

// parseJSONC parses JSON that may hold comments and trailing commas, as
// OpenCode accepts.
func parseJSONC(src []byte) (*jnode, error) {
	p := &jparser{s: src}
	if err := p.skip(); err != nil {
		return nil, err
	}
	n, err := p.value()
	if err != nil {
		return nil, err
	}
	if err := p.skip(); err != nil {
		return nil, err
	}
	if p.i != len(src) {
		return nil, p.errorf("unexpected content")
	}
	return n, nil
}

type jparser struct {
	s []byte
	i int
}

func (p *jparser) errorf(format string, args ...any) error {
	return fmt.Errorf("offset %d: %s", p.i, fmt.Sprintf(format, args...))
}

func (p *jparser) skip() error {
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.i++
		case c == '/' && p.i+1 < len(p.s) && p.s[p.i+1] == '/':
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		case c == '/' && p.i+1 < len(p.s) && p.s[p.i+1] == '*':
			end := bytes.Index(p.s[p.i+2:], []byte("*/"))
			if end < 0 {
				return p.errorf("unterminated comment")
			}
			p.i += 2 + end + 2
		default:
			return nil
		}
	}
	return nil
}

func (p *jparser) value() (*jnode, error) {
	if p.i >= len(p.s) {
		return nil, p.errorf("unexpected end of input")
	}
	start := p.i
	switch p.s[p.i] {
	case '{', '[':
		return p.container()
	case '"':
		if _, err := p.str(); err != nil {
			return nil, err
		}
		return &jnode{start: start, end: p.i}, nil
	}
	for p.i < len(p.s) && !strings.ContainsRune(",:[]{}\"/ \t\r\n", rune(p.s[p.i])) {
		p.i++
	}
	if p.i == start {
		return nil, p.errorf("unexpected %q", p.s[p.i])
	}
	return &jnode{start: start, end: p.i}, nil
}

func (p *jparser) container() (*jnode, error) {
	open, closing := p.s[p.i], byte('}')
	if open == '[' {
		closing = ']'
	}
	n := &jnode{kind: open, start: p.i}
	p.i++
	for {
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.i >= len(p.s) {
			return nil, p.errorf("unexpected end of input")
		}
		if p.s[p.i] == closing {
			p.i++
			n.end = p.i
			return n, nil
		}
		it := jitem{start: p.i}
		if open == '{' {
			if p.s[p.i] != '"' {
				return nil, p.errorf("expected a key")
			}
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			it.key = key
			if err := p.skip(); err != nil {
				return nil, err
			}
			if p.i >= len(p.s) || p.s[p.i] != ':' {
				return nil, p.errorf("expected ':'")
			}
			p.i++
			if err := p.skip(); err != nil {
				return nil, err
			}
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		it.val = v
		n.items = append(n.items, it)
		if err := p.skip(); err != nil {
			return nil, err
		}
		switch {
		case p.i < len(p.s) && p.s[p.i] == ',':
			p.i++
		case p.i < len(p.s) && p.s[p.i] == closing:
		default:
			return nil, p.errorf("expected ',' or %q", closing)
		}
	}
}

func (p *jparser) str() (string, error) {
	start := p.i
	for p.i++; p.i < len(p.s); p.i++ {
		switch p.s[p.i] {
		case '\\':
			p.i++
		case '\n':
			return "", p.errorf("unterminated string")
		case '"':
			p.i++
			var s string
			if err := json.Unmarshal(p.s[start:p.i], &s); err != nil {
				return "", p.errorf("invalid string")
			}
			return s, nil
		}
	}
	return "", p.errorf("unterminated string")
}

// lineIndent returns the whitespace before pos on its line; ok is false when
// something else precedes pos there.
func lineIndent(s []byte, pos int) (indent string, ok bool) {
	ls := bytes.LastIndexByte(s[:pos], '\n') + 1
	if len(bytes.TrimLeft(s[ls:pos], " \t")) != 0 {
		return "", false
	}
	return string(s[ls:pos]), true
}

// lineTail reports the newline ending pos's line when only blanks, one comma
// (its index, or -1), and a line comment follow pos.
func lineTail(s []byte, pos int) (nl, comma int, ok bool) {
	comma = -1
	for i := pos; i < len(s); i++ {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\r':
		case c == ',' && comma < 0:
			comma = i
		case c == '\n':
			return i, comma, true
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			end := bytes.IndexByte(s[i:], '\n')
			if end < 0 {
				return 0, comma, false
			}
			return i + end, comma, true
		default:
			return 0, comma, false
		}
	}
	return 0, comma, false
}

// commaAfter returns the index of the comma that follows pos, skipping blanks
// and comments, or -1.
func commaAfter(s []byte, pos int) int {
	p := &jparser{s: s, i: pos}
	if p.skip() != nil || p.i >= len(s) || s[p.i] != ',' {
		return -1
	}
	return p.i
}

func indentUnit(s []byte, root *jnode) string {
	if len(root.items) > 0 {
		if ind, ok := lineIndent(s, root.items[0].start); ok && ind != "" {
			return ind
		}
	}
	return "  "
}

// lineEOL returns the line ending of the line holding pos.
func lineEOL(s []byte, pos int) string {
	if i := bytes.IndexByte(s[pos:], '\n'); i >= 0 && pos+i > 0 && s[pos+i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

func splice(s []byte, from, to int, text string) []byte {
	out := make([]byte, 0, len(s)-(to-from)+len(text))
	out = append(out, s[:from]...)
	out = append(out, text...)
	return append(out, s[to:]...)
}

// renderItem renders key: v, or v alone when key is empty, where wrap names the
// objects argus creates around v, outermost first. With no unit it is one line,
// and v and each wrapping object are padded inside their brackets, so removeItem
// can tell a container argus created from an existing empty one argus filled.
func renderItem(key string, v any, wrap []string, prefix, unit string) (string, error) {
	var b []byte
	if unit == "" {
		var err error
		if b, err = renderJSON(v, "", ""); err != nil {
			return "", err
		}
		if len(b) > 2 && (b[0] == '{' || b[0] == '[') {
			b = []byte(string(b[:1]) + " " + string(b[1:len(b)-1]) + " " + string(b[len(b)-1:]))
		}
		for i := len(wrap) - 1; i >= 0; i-- {
			k, err := json.Marshal(wrap[i])
			if err != nil {
				return "", err
			}
			b = []byte("{ " + string(k) + ":" + string(b) + " }")
		}
	} else {
		for i := len(wrap) - 1; i >= 0; i-- {
			v = map[string]any{wrap[i]: v}
		}
		var err error
		if b, err = renderJSON(v, prefix, unit); err != nil {
			return "", err
		}
	}
	if key == "" {
		return string(b), nil
	}
	k, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	return string(k) + ": " + string(b), nil
}

// insertItem appends key: v (or the element v when key is empty), wrapped in the
// objects named by wrap, to container c. On a container with one item per line,
// the new item gets its own line, so removeItem restores the original bytes.
func insertItem(s []byte, c *jnode, key string, v any, wrap []string, unit string) ([]byte, error) {
	if len(c.items) == 0 {
		text, err := renderItem(key, v, wrap, "", "")
		if err != nil {
			return nil, err
		}
		return splice(s, c.start+1, c.start+1, text), nil
	}
	last := c.items[len(c.items)-1]
	indent, own := lineIndent(s, last.start)
	nl, comma, tail := lineTail(s, last.val.end)
	if !own || !tail {
		text, err := renderItem(key, v, wrap, "", "")
		if err != nil {
			return nil, err
		}
		return splice(s, last.val.end, last.val.end, ", "+text), nil
	}
	text, err := renderItem(key, v, wrap, indent, unit)
	if err != nil {
		return nil, err
	}
	eol := lineEOL(s, nl)
	text = eol + indent + strings.ReplaceAll(text, "\n", eol)
	nl -= len(eol) - 1
	if comma >= 0 {
		return splice(s, nl, nl, text+","), nil
	}
	s = splice(s, nl, nl, text)
	return splice(s, last.val.end, last.val.end, ","), nil
}

func replaceValue(s []byte, it *jitem, v any, unit string) ([]byte, error) {
	indent, own := lineIndent(s, it.start)
	if !own {
		unit = ""
	}
	text, err := renderJSON(v, indent, unit)
	if err != nil {
		return nil, err
	}
	return splice(s, it.val.start, it.val.end, strings.ReplaceAll(string(text), "\n", lineEOL(s, it.start))), nil
}

// removeItem deletes item it from container c, the inverse of insertItem.
func removeItem(s []byte, c *jnode, it *jitem) []byte {
	idx := 0
	for i := range c.items {
		if c.items[i].start == it.start {
			idx = i
		}
	}
	_, own := lineIndent(s, it.start)
	nl, comma, tail := lineTail(s, it.val.end)
	if own && tail {
		ls := bytes.LastIndexByte(s[:it.start], '\n') + 1
		if comma < 0 && idx > 0 {
			if pc := commaAfter(s, c.items[idx-1].val.end); pc >= 0 {
				s = splice(s, ls, nl+1, "")
				return splice(s, pc, pc+1, "")
			}
		}
		return splice(s, ls, nl+1, "")
	}
	if idx > 0 {
		// Drop the item and the comma before it, but not comments around that comma.
		from := it.start
		for from > 0 && (s[from-1] == ' ' || s[from-1] == '\t') {
			from--
		}
		s = splice(s, from, it.val.end, "")
		if pc := commaAfter(s, c.items[idx-1].val.end); pc >= 0 {
			s = splice(s, pc, pc+1, "")
		}
		return s
	}
	end := it.val.end
	if pc := commaAfter(s, end); pc >= 0 {
		end = pc + 1
		for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
			end++
		}
	}
	return splice(s, it.start, end, "")
}
