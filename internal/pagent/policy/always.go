package policy

import "strings"

// Opaque reports whether Argus cannot tell what the action does: no tool
// name, no input, a shell tool without a command, a file tool without a path
// or a fetch without a URL. Its classes are unknown.
func Opaque(a Action) bool {
	in := strings.TrimSpace(string(a.Input))
	if a.Tool == "" || in == "" || in == "null" || in == "{}" || in == `""` {
		return true
	}
	switch kindOf(a.Tool) {
	case kindShell:
		return len(shellCommands(a.Input)) == 0
	case kindFile:
		return len(filePaths(a.Tool, a.Input)) == 0
	case kindFetch:
		return a.Target == ""
	}
	return false
}

// AlwaysRule returns the narrow allow rule that covers only this action, for
// "always allow": the tool and the exact command of a single shell command,
// the one path of a file tool, the host of a fetch or the MCP tool. ok is
// false when no such rule exists: an opaque action, a shell chain or a
// command without arguments (whose rule would cover the program with any
// arguments), several paths, no target (the rule would cover every use of
// the tool), or a target with pattern characters.
func AlwaysRule(a Action) (Rule, bool) {
	if Opaque(a) || strings.ContainsAny(a.Tool, "*?") {
		return Rule{}, false
	}
	r := Rule{Tool: a.Tool, Decision: OutcomeAllow}
	switch kindOf(a.Tool) {
	case kindShell:
		cmds := shellCommands(a.Input)
		if len(cmds) != 1 || len(cmds[0].args) == 0 {
			return Rule{}, false
		}
		r.Target = cmds[0].String()
	case kindFile:
		paths := filePaths(a.Tool, a.Input)
		if len(paths) != 1 {
			return Rule{}, false
		}
		r.Target = paths[0]
	default:
		r.Target = a.Target
	}
	if r.Target == "" || strings.ContainsAny(r.Target, "*?") {
		return Rule{}, false
	}
	return r, true
}
