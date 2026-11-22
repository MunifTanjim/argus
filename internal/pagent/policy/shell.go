package policy

import (
	"path/filepath"
	"strings"
)

// command is one simple command of a shell line, with wrappers such as env and
// sudo stripped.
type command struct {
	prog string
	args []string
}

// String is the command as a space-joined line, used to match rule targets
// that name a subcommand ("gh pr *").
func (c command) String() string {
	return strings.TrimSpace(c.prog + " " + strings.Join(c.args, " "))
}

const maxShellDepth = 3

// parseShell returns the simple commands of a shell line in order. It handles
// quotes, escapes, the separators ; | & ( ) ` and newline, and looks inside
// `sh -c "..."`. It is not a full shell parser: expansions are not evaluated.
func parseShell(line string) []command {
	return parseShellDepth(line, 0)
}

func parseShellDepth(line string, depth int) []command {
	var out []command
	for _, words := range splitWords(line) {
		out = append(out, unwrap(words, depth)...)
	}
	return out
}

// commandFromArgv returns the commands of an argv-style invocation.
func commandFromArgv(argv []string) []command {
	return unwrap(argv, 0)
}

func splitWords(line string) [][]string {
	var (
		segs   [][]string
		cur    []string
		word   strings.Builder
		inWord bool
		quote  rune
	)
	endWord := func() {
		if inWord {
			cur = append(cur, word.String())
			word.Reset()
			inWord = false
		}
	}
	endSeg := func() {
		endWord()
		if len(cur) > 0 {
			segs = append(segs, cur)
			cur = nil
		}
	}
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(rs) && strings.ContainsRune("\"\\$`", rs[i+1]):
				i++
				word.WriteRune(rs[i])
			default:
				word.WriteRune(r)
			}
		case r == '\\':
			if i+1 < len(rs) {
				i++
				if rs[i] != '\n' {
					word.WriteRune(rs[i])
					inWord = true
				}
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\r':
			endWord()
		case strings.ContainsRune(";|&()`\n", r):
			endSeg()
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	endSeg()
	return segs
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ash": true, "ksh": true}

// optsWithArg lists, per wrapper, the options that consume the next word.
var optsWithArg = map[string]map[string]bool{
	"env":  {"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true},
	"sudo": {"-u": true, "-g": true, "-h": true, "-p": true, "-C": true, "-D": true, "-R": true, "-T": true, "-U": true},
	"doas": {"-u": true, "-C": true},
	"nice": {"-n": true},
}

func isAssignment(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return false
	}
	for j, r := range w[:i] {
		ok := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || j > 0 && r >= '0' && r <= '9'
		if !ok {
			return false
		}
	}
	return true
}

// unwrap strips env assignments and wrapper programs from words and returns
// the command they run (several, when it is a `sh -c` script).
func unwrap(words []string, depth int) []command {
	for len(words) > 0 {
		w := words[0]
		if isAssignment(w) {
			words = words[1:]
			continue
		}
		base := filepath.Base(w)
		switch base {
		case "env", "sudo", "doas", "nice":
			words = skipOptions(words[1:], optsWithArg[base])
			continue
		case "command", "nohup", "time", "exec", "setsid":
			words = skipOptions(words[1:], nil)
			continue
		}
		if shells[base] && depth < maxShellDepth {
			for i := 1; i < len(words); i++ {
				a := words[i]
				if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
					if i+1 < len(words) {
						return parseShellDepth(words[i+1], depth+1)
					}
					return nil
				}
				if !strings.HasPrefix(a, "-") {
					break
				}
			}
		}
		return []command{{prog: base, args: words[1:]}}
	}
	return nil
}

func skipOptions(words []string, withArg map[string]bool) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case w == "--":
			return words[1:]
		case strings.HasPrefix(w, "-") && len(w) > 1:
			if withArg[w] && len(words) > 1 {
				words = words[2:]
			} else {
				words = words[1:]
			}
		case isAssignment(w):
			words = words[1:]
		default:
			return words
		}
	}
	return words
}

// positional returns the non-option words of args, skipping the values of the
// options in withArg. It stops at "--".
func positional(args []string, withArg map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(out, args[i+1:]...)
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if withArg[a] {
				i++
			}
		default:
			out = append(out, a)
		}
	}
	return out
}

// hasShort reports whether args holds the short option r, alone or in a
// cluster such as -rf. Words after "--" are ignored.
func hasShort(args []string, r rune) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], r) {
			return true
		}
	}
	return false
}

// hasLong reports whether args holds one of the long options, with or without
// "=value".
func hasLong(args []string, names ...string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		n, _, _ := strings.Cut(a, "=")
		for _, want := range names {
			if n == want {
				return true
			}
		}
	}
	return false
}
