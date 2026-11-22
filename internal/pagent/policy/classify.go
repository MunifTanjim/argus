package policy

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// mcpOutward and mcpIrreversible are the name keywords that mark MCP tools.
// Argus classifies MCP tools by name only: Claude Code does not pass tool
// annotations such as destructiveHint in permission requests.
var (
	mcpOutward      = []string{"send", "post", "reply", "publish", "comment", "share", "invite"}
	mcpIrreversible = []string{"delete", "destroy", "drop", "purge", "archive"}
)

// Classify returns the warning classes of the action, in the fixed order
// outward, irreversible, security. A shell chain is classified by every
// program in it.
func Classify(a Action) []string {
	var outward, irreversible, security bool
	switch kindOf(a.Tool) {
	case kindShell:
		for _, c := range shellCommands(a.Input) {
			o, i, s := classifyCommand(c)
			outward, irreversible, security = outward || o, irreversible || i, security || s
		}
	case kindFile:
		for _, p := range filePaths(a.Tool, a.Input) {
			if sensitivePath(p) {
				security = true
			}
		}
	case kindFetch:
		m := strings.ToUpper(inputString(a.Input, "method"))
		outward = m != "" && m != "GET" && m != "HEAD" || inputString(a.Input, "body") != ""
	case kindMCP:
		words := nameWords(mcpTool(a.Tool))
		outward = containsAny(words, mcpOutward)
		irreversible = containsAny(words, mcpIrreversible) || destructiveHint(a.Input)
	}
	var out []string
	if outward {
		out = append(out, ClassOutward)
	}
	if irreversible {
		out = append(out, ClassIrreversible)
	}
	if security {
		out = append(out, ClassSecurity)
	}
	return out
}

func destructiveHint(in []byte) bool {
	m := inputObject(in)
	raw, ok := m["destructiveHint"]
	return ok && string(raw) == "true"
}

// nameWords splits a tool name into lowercase words at _, -, . and camelCase
// boundaries, so "postgres_query" does not count as "post".
func nameWords(name string) []string {
	var (
		out []string
		cur []rune
	)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == '.':
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) ||
			i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1])):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

func containsAny(words, keywords []string) bool {
	for _, w := range words {
		if slices.Contains(keywords, w) {
			return true
		}
	}
	return false
}

var (
	gitOptsWithArg = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true}
	ghOptsWithArg  = map[string]bool{"-R": true, "--repo": true}
)

func classifyCommand(c command) (outward, irreversible, security bool) {
	switch c.prog {
	case "git":
		pos := positional(c.args, gitOptsWithArg)
		if len(pos) == 0 {
			return
		}
		rest := c.args
		switch pos[0] {
		case "push":
			outward = true
			irreversible = gitForcePush(c.args)
		case "reset":
			irreversible = hasLong(rest, "--hard")
		case "clean":
			irreversible = true
		case "config":
			security = hasLong(rest, "--global", "--system")
		}
	case "gh":
		pos := positional(c.args, ghOptsWithArg)
		if len(pos) < 2 {
			return
		}
		switch pos[0] {
		case "pr":
			outward = pos[1] == "create" || pos[1] == "comment" || pos[1] == "merge"
		case "issue":
			outward = pos[1] == "create" || pos[1] == "comment"
		}
	case "rm":
		irreversible = (hasShort(c.args, 'r') || hasShort(c.args, 'R') || hasLong(c.args, "--recursive")) &&
			(hasShort(c.args, 'f') || hasLong(c.args, "--force"))
	case "curl":
		outward = curlSends(c.args)
	case "wget":
		outward = wgetSends(c.args)
	case "ssh":
		outward = true
	case "scp", "rsync":
		for _, a := range c.args {
			if remoteSpec(a) {
				outward = true
			}
		}
	case "ssh-keygen", "gpg", "gpg2":
		security = true
	case "chmod", "chown", "chgrp":
		for _, a := range c.args {
			if homePath(a) {
				security = true
			}
		}
	}
	return
}

func gitForcePush(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "--force") || a == "-f" || strings.HasPrefix(a, "+") ||
			strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "f") {
			return true
		}
	}
	return false
}

func curlSends(args []string) bool {
	longBody := []string{"--data", "--data-raw", "--data-binary", "--data-urlencode", "--data-ascii",
		"--form", "--form-string", "--upload-file", "--json"}
	shortWithArg := "HouAebcwxmE"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return false
		case strings.HasPrefix(a, "--"):
			n, v, hasV := strings.Cut(a, "=")
			if n == "--request" {
				if !hasV && i+1 < len(args) {
					i++
					v = args[i]
				}
				if nonRead(v) {
					return true
				}
				continue
			}
			for _, b := range longBody {
				if n == b {
					return true
				}
			}
			if n == "--header" || n == "--output" || n == "--user" || n == "--user-agent" {
				if !hasV {
					i++
				}
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for j, r := range a[1:] {
				if r == 'd' || r == 'F' || r == 'T' {
					return true
				}
				if r == 'X' {
					v := a[j+2:]
					if v == "" && i+1 < len(args) {
						i++
						v = args[i]
					}
					if nonRead(v) {
						return true
					}
					break
				}
				if strings.ContainsRune(shortWithArg, r) {
					if a[j+2:] == "" {
						i++
					}
					break
				}
			}
		}
	}
	return false
}

func nonRead(method string) bool {
	m := strings.ToUpper(method)
	return m != "" && m != "GET" && m != "HEAD"
}

func wgetSends(args []string) bool {
	for i, a := range args {
		n, v, hasV := strings.Cut(a, "=")
		switch n {
		case "--post-data", "--post-file", "--body-data", "--body-file":
			return true
		case "--method":
			if !hasV && i+1 < len(args) {
				v = args[i+1]
			}
			if nonRead(v) {
				return true
			}
		}
	}
	return false
}

// remoteSpec reports whether arg names a remote location: host:path,
// user@host:path or a URL with the rsync or ssh scheme.
func remoteSpec(arg string) bool {
	if strings.HasPrefix(arg, "rsync://") || strings.HasPrefix(arg, "ssh://") {
		return true
	}
	if strings.HasPrefix(arg, "-") {
		return false
	}
	i := strings.IndexByte(arg, ':')
	if i <= 0 {
		return false
	}
	host := arg[:i]
	return !strings.ContainsAny(host, "/") && !(len(host) == 1)
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// homePath reports whether p is, or is under, the user's home directory.
func homePath(p string) bool {
	for _, pre := range []string{"~", "$HOME", "${HOME}"} {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	h := homeDir()
	return h != "" && h != "/" && (p == h || strings.HasPrefix(p, h+"/"))
}

// sensitivePath reports whether editing p changes credentials: files under
// .ssh or .gnupg, and .env files.
func sensitivePath(p string) bool {
	p = filepath.ToSlash(filepath.Clean(p))
	for _, part := range strings.Split(p, "/") {
		if part == ".ssh" || part == ".gnupg" {
			return true
		}
	}
	base := filepath.Base(p)
	if base == ".env" {
		return true
	}
	if rest, ok := strings.CutPrefix(base, ".env."); ok {
		switch rest {
		case "example", "sample", "template", "dist":
			return false
		}
		return true
	}
	return false
}
