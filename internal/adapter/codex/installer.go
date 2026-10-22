package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/hookset"
	"github.com/MunifTanjim/argus/internal/atomicfile"
)

var predicate = hookset.Spec{Marker: hookset.ManagedMarker}

func codexHome() (string, error) {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func hooksJSONPath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hooks.json"), nil
}

func configTOMLPath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// load returns os.IsNotExist when the file is absent.
type store struct {
	path string
	load func() (hookset.Map, error)
	save func(hookset.Map) error
}

func specFor(s store) hookset.Spec {
	return hookset.Spec{Marker: hookset.ManagedMarker, Load: s.load, Save: s.save}
}

// For operations that span both files.
func stores() ([]store, error) {
	ts, err := tomlStore()
	if err != nil {
		return nil, err
	}
	js, err := jsonStore()
	if err != nil {
		return nil, err
	}
	return []store{ts, js}, nil
}

// removeLegacyHooks strips argus-managed hooks from both stores. removed reports
// whether any were present.
func removeLegacyHooks() (removed bool, err error) {
	all, err := stores()
	if err != nil {
		return false, err
	}
	for _, s := range all {
		hooks, err := s.load()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		if !predicate.AnyManaged(hooks) {
			continue
		}
		if err := specFor(s).Uninstall(); err != nil {
			return removed, err
		}
		removed = true
	}
	return removed, nil
}

// Uninstall removes argus's legacy hooks and its MCP server, leaving everything
// else untouched.
func Uninstall() error {
	if _, err := removeLegacyHooks(); err != nil {
		return err
	}
	_, err := removeMCPServer()
	return err
}

func jsonStore() (store, error) {
	path, err := hooksJSONPath()
	if err != nil {
		return store{}, err
	}
	return store{
		path: path,
		load: func() (hookset.Map, error) {
			top, err := readJSONTop(path)
			if err != nil {
				return nil, err
			}
			return decodeJSONHooks(top)
		},
		save: func(hooks hookset.Map) error { return saveJSONHooks(path, hooks) },
	}, nil
}

// Missing file returns os.IsNotExist; empty file yields an empty map.
func readJSONTop(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	top := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return top, nil
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return top, nil
}

func decodeJSONHooks(top map[string]json.RawMessage) (hookset.Map, error) {
	hooks := hookset.Map{}
	if raw, ok := top["hooks"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("parse hooks: %w", err)
		}
	}
	return hooks, nil
}

// Removes the file when empty.
func saveJSONHooks(path string, hooks hookset.Map) error {
	top, err := readJSONTop(path)
	if os.IsNotExist(err) {
		top = map[string]json.RawMessage{}
	} else if err != nil {
		return err
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		raw, err := hookset.MarshalNoEscape(hooks)
		if err != nil {
			return err
		}
		top["hooks"] = raw
	}
	if len(top) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	out, err := hookset.MarshalIndentNoEscape(top)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func tomlStore() (store, error) {
	path, err := configTOMLPath()
	if err != nil {
		return store{}, err
	}
	return store{
		path: path,
		load: func() (hookset.Map, error) {
			top, err := readTOMLTop(path)
			if err != nil {
				return nil, err
			}
			defs, _ := splitHooksTable(top["hooks"])
			return hooksFromAny(defs)
		},
		save: func(hooks hookset.Map) error { return saveTOMLHooks(path, hooks) },
	}, nil
}

// Missing file returns os.IsNotExist; empty file yields an empty map.
func readTOMLTop(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	top, err := decodeTOML(b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return top, nil
}

// Preserves non-definition children and the rest of the config.
func saveTOMLHooks(path string, hooks hookset.Map) error {
	top, err := readTOMLTop(path)
	if os.IsNotExist(err) {
		top = map[string]any{}
	} else if err != nil {
		return err
	}

	_, other := splitHooksTable(top["hooks"])
	merged := map[string]any{}
	for k, v := range other {
		merged[k] = v
	}
	if len(hooks) > 0 {
		defs, err := anyFromHooks(hooks)
		if err != nil {
			return err
		}
		if dm, ok := defs.(map[string]any); ok {
			for k, v := range dm {
				merged[k] = v
			}
		}
	}
	if len(merged) == 0 {
		delete(top, "hooks")
	} else {
		top["hooks"] = merged
	}

	out, err := toml.Marshal(top)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// Event definitions are array-valued children.
func splitHooksTable(v any) (defs, other map[string]any) {
	defs, other = map[string]any{}, map[string]any{}
	m, ok := v.(map[string]any)
	if !ok {
		return defs, other
	}
	for k, val := range m {
		if _, isArray := val.([]any); isArray {
			defs[k] = val
		} else {
			other[k] = val
		}
	}
	return defs, other
}

func hooksFromAny(v any) (hookset.Map, error) {
	if v == nil {
		return hookset.Map{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := hookset.Map{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func anyFromHooks(m hookset.Map) (any, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func mcpServerValue(url, token string) map[string]any {
	return map[string]any{
		"url":                         url,
		"http_headers":                map[string]any{"Authorization": "Bearer " + token},
		"default_tools_approval_mode": "approve",
	}
}

func mcpServerTable(url, token string) string {
	return "[mcp_servers.argus]\n" +
		"url = " + tomlString(url) + "\n" +
		"http_headers = { Authorization = " + tomlString("Bearer "+token) + " }\n" +
		"default_tools_approval_mode = \"approve\"\n"
}

// writeMCPServer adds or updates the argus table in config.toml. It reports
// ErrAgentNotInstalled when the Codex home does not exist.
func writeMCPServer(url, token string) (changed bool, err error) {
	return editMCPServer(func(src []byte, cur map[string]any) ([]byte, map[string]any, bool) {
		want := mcpServerValue(url, token)
		if reflect.DeepEqual(argusServer(cur), want) {
			return src, want, false
		}
		return setArgusTable(src, mcpServerTable(url, token)), want, true
	})
}

// mcpServerURL is the url of the argus server in config.toml, or "".
func mcpServerURL() string {
	path, err := configTOMLPath()
	if err != nil {
		return ""
	}
	cur, err := readTOMLTop(path)
	if err != nil {
		return ""
	}
	url, _ := argusServer(cur)["url"].(string)
	return url
}

func removeMCPServer() (changed bool, err error) {
	changed, err = editMCPServer(func(src []byte, cur map[string]any) ([]byte, map[string]any, bool) {
		if argusServer(cur) == nil {
			return src, nil, false
		}
		return setArgusTable(src, ""), nil, true
	})
	if errors.Is(err, adapter.ErrAgentNotInstalled) {
		return false, nil
	}
	return changed, err
}

// editMCPServer edits config.toml as text so comments and formatting survive, and
// refuses to write unless the result differs from the original only in the argus
// server. It reports ErrAgentNotInstalled when the Codex home does not exist.
func editMCPServer(edit func(src []byte, cur map[string]any) (out []byte, want map[string]any, changed bool)) (bool, error) {
	path, err := configTOMLPath()
	if err != nil {
		return false, err
	}
	if !adapter.DirExists(filepath.Dir(path)) {
		return false, adapter.ErrAgentNotInstalled
	}
	src, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	cur, err := decodeTOML(src)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	out, want, changed := edit(src, cur)
	if want != nil {
		if err := adapter.RestrictTokenFile(Agent, path); err != nil {
			return false, err
		}
	}
	if !changed {
		return false, nil
	}
	got, err := decodeTOML(out)
	if err != nil {
		return false, fmt.Errorf("edit %s: %w", path, err)
	}
	if !reflect.DeepEqual(withoutArgusServer(cur), withoutArgusServer(got)) || !reflect.DeepEqual(argusServer(got), want) {
		return false, fmt.Errorf("edit %s: [mcp_servers.argus] is defined in a form argus cannot edit", path)
	}
	if err := atomicfile.Replace(path, out); err != nil {
		return false, err
	}
	return true, nil
}

func decodeTOML(b []byte) (map[string]any, error) {
	top := map[string]any{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return top, nil
	}
	if err := toml.Unmarshal(b, &top); err != nil {
		return nil, err
	}
	return top, nil
}

func argusServer(top map[string]any) map[string]any {
	servers, _ := top["mcp_servers"].(map[string]any)
	s, _ := servers["argus"].(map[string]any)
	return s
}

func withoutArgusServer(top map[string]any) map[string]any {
	servers, ok := top["mcp_servers"].(map[string]any)
	if !ok {
		return top
	}
	out := make(map[string]any, len(top))
	for k, v := range top {
		out[k] = v
	}
	rest := make(map[string]any, len(servers))
	for k, v := range servers {
		if k != "argus" {
			rest[k] = v
		}
	}
	if len(rest) == 0 {
		delete(out, "mcp_servers")
	} else {
		out["mcp_servers"] = rest
	}
	return out
}

// setArgusTable replaces the [mcp_servers.argus] table and its sub-tables with
// table, appending it when absent; an empty table removes them. The file's line
// endings and its final newline, or lack of one, are kept.
func setArgusTable(src []byte, table string) []byte {
	s := string(src)
	eol := "\n"
	if strings.Contains(s, "\r\n") {
		eol = "\r\n"
	}
	table = strings.ReplaceAll(table, "\n", eol)
	lines := strings.SplitAfter(s, "\n")
	blocks := argusTableBlocks(lines)
	if len(blocks) == 0 {
		switch {
		case table == "":
			return src
		case s == "":
			return []byte(table)
		case !strings.HasSuffix(s, "\n"):
			return []byte(s + eol + eol + strings.TrimSuffix(table, eol))
		}
		return []byte(s + eol + table)
	}
	drop := make([]bool, len(lines))
	for i, b := range blocks {
		for j := b.start; j < b.end; j++ {
			drop[j] = true
		}
		if table != "" && i == 0 {
			continue
		}
		// The blank line that separated the table goes with it.
		if b.start > 0 && isBlank(lines[b.start-1]) {
			drop[b.start-1] = true
		} else if b.start == 0 && b.end < len(lines) && isBlank(lines[b.end]) && lines[b.end] != "" {
			drop[b.end] = true
		}
	}
	var out strings.Builder
	for i, l := range lines {
		if table != "" && i == blocks[0].start {
			out.WriteString(table)
		}
		if !drop[i] {
			out.WriteString(l)
		}
	}
	res := out.String()
	if s != "" && !strings.HasSuffix(s, "\n") {
		res = strings.TrimSuffix(strings.TrimSuffix(res, "\n"), "\r")
	}
	return []byte(res)
}

type lineRange struct{ start, end int }

// argusTableBlocks returns runs of argus table lines, each from its header to
// its last key line; comments and blank lines after that belong to what follows.
func argusTableBlocks(lines []string) []lineRange {
	headers := tomlHeaders(lines)
	var blocks []lineRange
	in, last := false, 0
	for i, l := range lines {
		if path, ok := headers[i]; ok {
			argus := len(path) >= 2 && path[0] == "mcp_servers" && path[1] == "argus"
			if in && !argus {
				blocks[len(blocks)-1].end = last + 1
				in = false
			}
			if argus {
				if !in {
					blocks = append(blocks, lineRange{start: i})
					in = true
				}
				last = i
			}
			continue
		}
		if in && !isBlank(l) && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			last = i
		}
	}
	if in {
		blocks[len(blocks)-1].end = last + 1
	}
	return blocks
}

// tomlHeaders maps line indexes to the key paths of the table headers on them.
// Lines inside multi-line strings, arrays, and inline tables are not headers.
func tomlHeaders(lines []string) map[int][]string {
	headers := map[int][]string{}
	var st tomlScan
	for i, l := range lines {
		if st.depth == 0 && st.ml == "" {
			if path, ok := tomlHeader(l); ok {
				headers[i] = path
				continue
			}
		}
		st.scan(l)
	}
	return headers
}

// tomlScan tracks the open brackets and the multi-line string a value line
// leaves open.
type tomlScan struct {
	depth int
	ml    string // closing delimiter of the open multi-line string
}

func (st *tomlScan) scan(line string) {
	for i := 0; i < len(line); i++ {
		if st.ml != "" {
			if strings.HasPrefix(line[i:], st.ml) {
				i += len(st.ml) - 1
				st.ml = ""
			} else if st.ml == `"""` && line[i] == '\\' {
				i++
			}
			continue
		}
		switch c := line[i]; c {
		case '#':
			return
		case '"', '\'':
			if q := strings.Repeat(string(c), 3); strings.HasPrefix(line[i:], q) {
				st.ml = q
				i += len(q) - 1
				continue
			}
			for i++; i < len(line) && line[i] != c; i++ {
				if c == '"' && line[i] == '\\' {
					i++
				}
			}
		case '[', '{':
			st.depth++
		case ']', '}':
			if st.depth > 0 {
				st.depth--
			}
		}
	}
}

// tomlHeader accepts both [table] and [[array]] headers.
func tomlHeader(line string) ([]string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "["), "[")
	var path []string
	var key strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'':
			end := i + 1
			for end < len(s) && s[end] != c {
				if c == '"' && s[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(s) {
				return nil, false
			}
			part := s[i+1 : end]
			if c == '"' {
				u, err := strconv.Unquote(s[i : end+1])
				if err != nil {
					return nil, false
				}
				part = u
			}
			key.WriteString(part)
			i = end
		case '.', ']':
			path = append(path, strings.TrimSpace(key.String()))
			key.Reset()
			if c == ']' {
				return path, true
			}
		default:
			key.WriteByte(c)
		}
	}
	return nil, false
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }
