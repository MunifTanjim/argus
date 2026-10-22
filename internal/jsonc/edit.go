// Package jsonc edits the entries argus owns in JSON configs that may hold
// comments and trailing commas, keeping every other byte of the file.
package jsonc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

// Entry is one value argus owns in a config whose root is an object. Path names
// the objects from the root to the container: an object holding Key, or, when
// Key is empty, an array holding Value as an element.
type Entry struct {
	Path  []string
	Key   string
	Value any
}

func (e Entry) name() string {
	if e.Key == "" {
		return strings.Join(e.Path, ".")
	}
	return strings.Join(append(append([]string{}, e.Path...), e.Key), ".")
}

// Set adds or updates entries in src. Containers on an entry's path are
// created as needed. It fails, returning no output, when the result would
// differ from src in anything but the entries.
func Set(src []byte, entries ...Entry) ([]byte, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		root := map[string]any{}
		for _, e := range entries {
			setDecoded(root, e)
		}
		out, err := renderJSON(root, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(out, '\n'), nil
	}
	out := src
	for _, e := range entries {
		var err error
		if out, err = set(out, e); err != nil {
			return nil, err
		}
	}
	return out, verify(src, out, entries, true)
}

func set(s []byte, e Entry) ([]byte, error) {
	if len(e.Path) == 0 && e.Key == "" {
		return nil, errors.New("an element entry needs a path")
	}
	root, err := parseRoot(s)
	if err != nil {
		return nil, err
	}
	unit := indentUnit(s, root)
	c := root
	for i, name := range e.Path {
		m := c.member(name)
		if m == nil {
			return insertItem(s, c, name, leaf(e), e.Path[i+1:], unit)
		}
		want := byte('{')
		if i == len(e.Path)-1 && e.Key == "" {
			want = '['
		}
		if m.val.kind != want {
			kind := "an object"
			if want == '[' {
				kind = "an array"
			}
			return nil, fmt.Errorf("%s is not %s", strings.Join(e.Path[:i+1], "."), kind)
		}
		c = m.val
	}
	if e.Key == "" {
		if c.index(s, e.Value) >= 0 {
			return s, nil
		}
		return insertItem(s, c, "", e.Value, nil, unit)
	}
	switch it := c.member(e.Key); {
	case it == nil:
		return insertItem(s, c, e.Key, e.Value, nil, unit)
	case !it.val.equal(s, e.Value):
		return replaceValue(s, it, e.Value, unit)
	}
	return s, nil
}

// leaf is the innermost container argus creates for e.
func leaf(e Entry) any {
	if e.Key == "" {
		return []any{e.Value}
	}
	return map[string]any{e.Key: e.Value}
}

func setDecoded(root map[string]any, e Entry) {
	m := root
	for i, name := range e.Path {
		if i == len(e.Path)-1 && e.Key == "" {
			a, _ := m[name].([]any)
			m[name] = append(a, e.Value)
			return
		}
		next, ok := m[name].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[name] = next
		}
		m = next
	}
	m[e.Key] = e.Value
}

// Unset removes entries from src: a member whatever its value, and every
// element equal to Value. A container argus created for an entry goes with it;
// with dropRoot, so does a root object argus created in an empty file, which
// leaves the file empty. It fails, returning no output, when the result would
// differ from src in anything but the entries.
func Unset(src []byte, dropRoot bool, entries ...Entry) ([]byte, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		return src, nil
	}
	out := src
	for _, e := range entries {
		for len(bytes.TrimSpace(out)) > 0 {
			next, removed, err := unset(out, e, dropRoot)
			if err != nil {
				return nil, err
			}
			if !removed {
				break
			}
			out = next
		}
	}
	return out, verify(src, out, entries, false)
}

// unset removes one occurrence of e.
func unset(s []byte, e Entry, dropRoot bool) ([]byte, bool, error) {
	root, err := parseRoot(s)
	if err != nil {
		return nil, false, err
	}
	containers := []*jnode{root}
	var items []*jitem
	for i, name := range e.Path {
		m := containers[i].member(name)
		if m == nil || m.val.kind == 0 {
			return nil, false, nil
		}
		items = append(items, m)
		containers = append(containers, m.val)
	}
	c := containers[len(containers)-1]
	if e.Key == "" {
		i := -1
		if c.kind == '[' {
			i = c.index(s, e.Value)
		}
		if i < 0 {
			return nil, false, nil
		}
		items = append(items, &c.items[i])
	} else {
		it := c.member(e.Key)
		if it == nil {
			return nil, false, nil
		}
		items = append(items, it)
	}
	k := len(items) - 1
	for k > 0 && created(containers[k]) {
		k--
	}
	if k == 0 && dropRoot && created(root) {
		return removeRoot(s, root), true, nil
	}
	return removeItem(s, containers[k], items[k]), true, nil
}

// created reports whether argus created container c: it holds one item, which
// does not start right after the opening bracket (see renderItem).
func created(c *jnode) bool {
	return len(c.items) == 1 && c.items[0].start != c.start+1
}

// removeRoot empties a file whose only content is the root, and otherwise
// leaves an empty root object.
func removeRoot(s []byte, root *jnode) []byte {
	rest := splice(s, root.start, root.end, "")
	if len(bytes.TrimSpace(rest)) == 0 {
		return []byte{}
	}
	return splice(s, root.start, root.end, "{}")
}

// verify checks that after differs from before only in entries, and that they
// are present (set) or absent.
func verify(before, after []byte, entries []Entry, set bool) error {
	b, err := Decode(before)
	if err != nil {
		return err
	}
	a, err := Decode(after)
	if err != nil {
		return fmt.Errorf("edited config does not parse: %w", err)
	}
	for _, e := range entries {
		got, found := strip(a, e)
		switch {
		case set && (!found || e.Key != "" && !reflect.DeepEqual(got, toAny(e.Value))):
			return fmt.Errorf("%s is missing after the edit", e.name())
		case !set && found:
			return fmt.Errorf("%s remains after the edit", e.name())
		}
		strip(b, e)
	}
	if !reflect.DeepEqual(a, b) {
		return errors.New("edit would change entries argus does not own")
	}
	return nil
}

// strip removes e from the decoded config cfg, then the containers on its path
// that are empty, whether or not e was there. It returns the removed member
// value, and whether e was there.
func strip(cfg map[string]any, e Entry) (value any, found bool) {
	parents := []map[string]any{cfg}
	m := cfg
	for i, name := range e.Path {
		if i == len(e.Path)-1 && e.Key == "" {
			if a, ok := m[name].([]any); ok {
				want := toAny(e.Value)
				var rest []any
				for _, v := range a {
					if reflect.DeepEqual(v, want) {
						found = true
					} else {
						rest = append(rest, v)
					}
				}
				m[name] = rest
			}
			break
		}
		next, ok := m[name].(map[string]any)
		if !ok {
			break
		}
		m = next
		parents = append(parents, m)
	}
	if e.Key != "" && len(parents) == len(e.Path)+1 {
		value, found = m[e.Key]
		delete(m, e.Key)
	}
	for i := min(len(parents), len(e.Path)) - 1; i >= 0; i-- {
		v, ok := parents[i][e.Path[i]]
		if !ok {
			continue
		}
		switch v := v.(type) {
		case map[string]any:
			if len(v) > 0 {
				return value, found
			}
		case []any:
			if len(v) > 0 {
				return value, found
			}
		default:
			return value, found
		}
		delete(parents[i], e.Path[i])
	}
	return value, found
}

// Decode parses src into its root object; blank input is an empty object.
func Decode(src []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		return map[string]any{}, nil
	}
	root, err := parseRoot(src)
	if err != nil {
		return nil, err
	}
	v, err := root.decode(src)
	if err != nil {
		return nil, err
	}
	return v.(map[string]any), nil
}

// LookupString returns the non-empty string at path in the config at file, or
// "" when the file cannot be read or holds no such string.
func LookupString(file string, path ...string) string {
	src, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	cfg, err := Decode(src)
	if err != nil {
		return ""
	}
	var v any = cfg
	for _, k := range path {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	s, _ := v.(string)
	return s
}

func parseRoot(src []byte) (*jnode, error) {
	root, err := parseJSONC(src)
	if err != nil {
		return nil, err
	}
	if root.kind != '{' {
		return nil, errors.New("config is not a JSON object")
	}
	return root, nil
}

// EditFile applies edit to the file at path, a missing file reading as empty,
// and replaces the file only when the content changes. It never creates
// directories.
func EditFile(path string, edit func(src []byte) ([]byte, error)) (changed bool, err error) {
	src, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	out, err := edit(src)
	if err != nil {
		return false, fmt.Errorf("edit %s: %w", path, err)
	}
	if bytes.Equal(out, src) {
		return false, nil
	}
	if err := atomicfile.Replace(path, out); err != nil {
		return false, err
	}
	return true, nil
}
