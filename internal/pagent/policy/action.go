// Package policy describes what an agent session is about to do and decides
// whether it may.
package policy

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// Classes of action that warrant a warning, computed by Argus.
const (
	ClassOutward      = "outward"
	ClassIrreversible = "irreversible"
	ClassSecurity     = "security"
)

// Action is one operation an agent CLI asks about.
type Action struct {
	// Tool is the CLI's tool name ("Bash", "Edit", "mcp__server__tool").
	Tool string
	// Target is the program for shell tools (the first of a chain), the path
	// for file tools, the host for fetches and the tool for MCP.
	Target string
	// Class lists the warning classes of the action.
	Class []string

	StartedBy string
	AgentID   string
	ThreadID  string
	SessionID string

	// Input is the tool input as the CLI sent it.
	Input json.RawMessage
}

type toolKind int

const (
	kindOther toolKind = iota
	kindShell
	kindFile
	kindFetch
	kindMCP
)

func kindOf(tool string) toolKind {
	t := strings.ToLower(tool)
	switch {
	case strings.HasPrefix(t, "mcp__"):
		return kindMCP
	}
	switch t {
	case "bash", "shell", "exec", "exec_command", "shell_command", "local_shell", "run_shell_command", "execute":
		return kindShell
	case "edit", "write", "multiedit", "notebookedit", "apply_patch", "patch":
		return kindFile
	case "webfetch":
		return kindFetch
	}
	return kindOther
}

// Describe builds the action for a tool request of an agent CLI (claude,
// codex, opencode, pi). The Class is computed here, never taken from the
// model.
func Describe(cli, toolName string, toolInput json.RawMessage) Action {
	a := Action{Tool: toolName, Input: toolInput}
	switch kindOf(toolName) {
	case kindShell:
		if cmds := shellCommands(toolInput); len(cmds) > 0 {
			a.Target = cmds[0].prog
		}
	case kindFile:
		if paths := filePaths(toolName, toolInput); len(paths) > 0 {
			a.Target = paths[0]
		}
	case kindFetch:
		if raw := inputString(toolInput, "url"); raw != "" {
			a.Target = hostOf(raw)
		}
	case kindMCP:
		a.Target = mcpTool(toolName)
	}
	a.Class = Classify(a)
	return a
}

func mcpTool(name string) string {
	rest := strings.TrimPrefix(name, "mcp__")
	if _, tool, ok := strings.Cut(rest, "__"); ok {
		return tool
	}
	return rest
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		if u2, err := url.Parse("https://" + raw); err == nil {
			return strings.ToLower(u2.Hostname())
		}
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// inputObject decodes the input as a JSON object, or nil.
func inputObject(in json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(in, &m) != nil {
		return nil
	}
	return m
}

// inputString returns the first of keys holding a string in the input.
func inputString(in json.RawMessage, keys ...string) string {
	m := inputObject(in)
	for _, k := range keys {
		var s string
		if raw, ok := m[k]; ok && json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
	}
	return ""
}

// shellCommands extracts the commands of a shell tool input: {command: "..."},
// {command: [argv...]}, {cmd: "..."}, or the bare command line.
func shellCommands(in json.RawMessage) []command {
	trimmed := strings.TrimSpace(string(in))
	if trimmed == "" {
		return nil
	}
	if m := inputObject(in); m != nil {
		for _, k := range []string{"command", "cmd", "script"} {
			raw, ok := m[k]
			if !ok {
				continue
			}
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return parseShell(s)
			}
			var argv []string
			if json.Unmarshal(raw, &argv) == nil && len(argv) > 0 {
				return commandFromArgv(argv)
			}
		}
		return nil
	}
	var s string
	if json.Unmarshal(in, &s) == nil {
		return parseShell(s)
	}
	var argv []string
	if json.Unmarshal(in, &argv) == nil {
		return commandFromArgv(argv)
	}
	return parseShell(trimmed)
}

var patchPath = regexp.MustCompile(`(?m)^\*\*\* (?:Add File|Update File|Delete File|Move to): (.+?)\s*$`)

// filePaths returns the paths a file tool touches.
func filePaths(tool string, in json.RawMessage) []string {
	if strings.EqualFold(tool, "apply_patch") || strings.EqualFold(tool, "patch") {
		text := inputString(in, "input", "patch", "diff", "command")
		if text == "" {
			var s string
			if json.Unmarshal(in, &s) == nil {
				text = s
			} else if inputObject(in) == nil {
				text = string(in)
			}
		}
		var out []string
		for _, m := range patchPath.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1])
		}
		return out
	}
	if p := inputString(in, "file_path", "filePath", "notebook_path", "path", "filename"); p != "" {
		return []string{p}
	}
	return nil
}
