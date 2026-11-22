// Package pagent implements persistent agents: a definition file, a
// directory scaffold, and the settings an agent resolves from config.
package pagent

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"
)

// DefinitionFile is the name of the definition inside an agent directory.
const DefinitionFile = "agent.yaml"

// Definition is the content of agent.yaml. Keys it does not know are kept
// when it is saved again.
type Definition struct {
	ID                    string        `yaml:"id,omitempty"`
	Name                  string        `yaml:"name,omitempty"`
	Description           string        `yaml:"description,omitempty"`
	Instructions          string        `yaml:"instructions,omitempty"`
	Agent                 string        `yaml:"agent,omitempty"`
	Model                 string        `yaml:"model,omitempty"`
	Workdirs              []string      `yaml:"workdirs,omitempty"`
	Skills                []string      `yaml:"skills,omitempty"`
	Timezone              string        `yaml:"timezone,omitempty"`
	Memory                MemoryDef     `yaml:"memory,omitempty"`
	IdleClose             time.Duration `yaml:"idle-close,omitempty"`
	MaxConcurrentSessions int           `yaml:"max-concurrent-sessions,omitempty"`
	Notify                string        `yaml:"notify,omitempty"`
	Proactivity           string        `yaml:"proactivity,omitempty"`
	CanMessage            []string      `yaml:"can-message,omitempty"`

	// doc is the parsed file this definition was loaded from.
	doc *yaml.Node
}

type MemoryDef struct {
	Mode         string        `yaml:"mode,omitempty"`
	CaptureAfter time.Duration `yaml:"capture-after,omitempty"`
	Extractor    string        `yaml:"extractor,omitempty"`
}

// knownKeys are the top-level keys that Definition manages.
var knownKeys = []string{
	"id", "name", "description", "instructions", "agent", "model", "workdirs",
	"skills", "timezone", "memory", "idle-close", "max-concurrent-sessions",
	"notify", "proactivity", "can-message",
}

// LoadDefinition reads agent.yaml from dir.
func LoadDefinition(dir string) (*Definition, error) {
	data, err := os.ReadFile(filepath.Join(dir, DefinitionFile))
	if err != nil {
		return nil, err
	}
	var d Definition
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", DefinitionFile, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", DefinitionFile, err)
	}
	d.doc = &doc
	return &d, nil
}

// Save writes agent.yaml into dir. Unknown keys and comments of the file that
// d was loaded from, or of the file already in dir, are kept.
func (d *Definition) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, DefinitionFile)
	doc := d.doc
	if doc == nil {
		if data, err := os.ReadFile(path); err == nil {
			doc = &yaml.Node{}
			if err := yaml.Unmarshal(data, doc); err != nil {
				return fmt.Errorf("parse %s: %w", DefinitionFile, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	var fresh yaml.Node
	if err := fresh.Encode(d); err != nil {
		return err
	}
	if doc == nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&fresh}}
	} else {
		mergeKnown(doc.Content[0], &fresh)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	d.doc = doc
	return nil
}

// mergeKnown applies the known keys of fresh onto orig: changed values are
// replaced, unchanged ones keep their comments, cleared ones are removed.
func mergeKnown(orig, fresh *yaml.Node) {
	freshVal := map[string]*yaml.Node{}
	for i := 0; i+1 < len(fresh.Content); i += 2 {
		freshVal[fresh.Content[i].Value] = fresh.Content[i+1]
	}
	var out []*yaml.Node
	seen := map[string]bool{}
	for i := 0; i+1 < len(orig.Content); i += 2 {
		k, v := orig.Content[i], orig.Content[i+1]
		if !slices.Contains(knownKeys, k.Value) {
			out = append(out, k, v)
			continue
		}
		nv, ok := freshVal[k.Value]
		if !ok {
			continue
		}
		seen[k.Value] = true
		if !sameNode(v, nv) {
			nv.HeadComment, nv.LineComment, nv.FootComment = v.HeadComment, v.LineComment, v.FootComment
			v = nv
		}
		out = append(out, k, v)
	}
	for i := 0; i+1 < len(fresh.Content); i += 2 {
		if k := fresh.Content[i]; !seen[k.Value] {
			out = append(out, k, fresh.Content[i+1])
		}
	}
	orig.Content = out
}

func sameNode(a, b *yaml.Node) bool {
	ab, err1 := yaml.Marshal(stripComments(a))
	bb, err2 := yaml.Marshal(stripComments(b))
	return err1 == nil && err2 == nil && bytes.Equal(ab, bb)
}

func stripComments(n *yaml.Node) *yaml.Node {
	c := *n
	c.HeadComment, c.LineComment, c.FootComment = "", "", ""
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = stripComments(ch)
	}
	return &c
}

const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// NewID returns "ag_" followed by 26 lowercase base32 characters: a
// millisecond timestamp prefix, then random bits.
func NewID() string {
	var b [26]byte
	ms := uint64(time.Now().UnixMilli())
	for i := 9; i >= 0; i-- {
		b[i] = idAlphabet[ms&31]
		ms >>= 5
	}
	var r [16]byte
	if _, err := rand.Read(r[:]); err != nil {
		panic(err)
	}
	for i, v := range r {
		b[10+i] = idAlphabet[v&31]
	}
	return "ag_" + string(b[:])
}
