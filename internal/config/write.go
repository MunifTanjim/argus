package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

// SetValues sets dotted-path keys in the YAML config at path, creating the
// file if absent and preserving existing comments and key order. Keys already
// set to an equal value are left alone. Keys set to a different value are
// returned as conflicts and nothing is written, unless force is true.
func SetValues(path string, values map[string]any, force bool) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	var doc yaml.Node
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
		// A comment-only file parses to an empty document; keep its comments.
		doc.Content[0].HeadComment = strings.TrimSpace(string(data))
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: top level is not a mapping", path)
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var conflicts []string
	changed := false
	for _, key := range keys {
		res, err := setPath(root, strings.Split(key, "."), values[key], force)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		switch res {
		case conflict:
			conflicts = append(conflicts, key)
		case modified:
			changed = true
		}
	}
	if len(conflicts) > 0 {
		return conflicts, nil
	}
	if !changed {
		return nil, nil
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	if data == nil {
		return nil, atomicfile.Write(path, buf.Bytes())
	}
	return nil, atomicfile.Replace(path, buf.Bytes())
}

type setResult int

const (
	unchanged setResult = iota
	modified
	conflict // an existing different value; only without force, which overwrites it
)

func setPath(m *yaml.Node, parts []string, val any, force bool) (setResult, error) {
	name := parts[0]
	var child *yaml.Node
	var idx int
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == name {
			child, idx = m.Content[i+1], i+1
			break
		}
	}

	if len(parts) > 1 {
		switch {
		case child == nil:
			child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, child)
		case child.Kind != yaml.MappingNode:
			if !force {
				return conflict, nil
			}
			*child = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		// A new or replaced mapping is empty, so the leaf below is always added.
		return setPath(child, parts[1:], val, force)
	}

	newNode := &yaml.Node{}
	if err := newNode.Encode(val); err != nil {
		return unchanged, err
	}
	switch {
	case child == nil:
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, newNode)
		return modified, nil
	case scalarEquals(child, val):
		return unchanged, nil
	case !force:
		return conflict, nil
	}
	newNode.HeadComment, newNode.LineComment, newNode.FootComment = child.HeadComment, child.LineComment, child.FootComment
	m.Content[idx] = newNode
	return modified, nil
}

func scalarEquals(n *yaml.Node, val any) bool {
	if n.Kind != yaml.ScalarNode {
		return false
	}
	got := reflect.New(reflect.TypeOf(val))
	if err := n.Decode(got.Interface()); err != nil {
		return false
	}
	return reflect.DeepEqual(got.Elem().Interface(), val)
}
