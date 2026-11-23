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
	// opaque marks a command the parser cannot read: the program is an
	// expansion, the script is not given, or the line is malformed. Allow
	// rules never match it and it is classified as outward.
	opaque bool
}

// String is the command as a space-joined line, used to match rule targets
// that name a subcommand ("gh pr *").
func (c command) String() string {
	return strings.TrimSpace(c.prog + " " + strings.Join(c.args, " "))
}

const maxShellDepth = 3

// parseShell returns the simple commands of a shell line in order. It handles
// quotes, escapes, the separators ; | & ( ) ` and newline, command
// substitutions inside double quotes, compound-command keywords, and looks
// inside `sh -c "..."`, eval, xargs and find -exec. It is not a full shell
// parser: expansions are not evaluated.
func parseShell(line string) []command {
	return parseShellDepth(line, 0)
}

func parseShellDepth(line string, depth int) []command {
	if depth > maxShellDepth {
		return []command{{prog: line, opaque: true}}
	}
	segs, subs, bad := splitWords(line)
	var out []command
	for _, words := range segs {
		out = append(out, unwrap(words, depth)...)
	}
	for _, sub := range subs {
		out = append(out, parseShellDepth(sub, depth+1)...)
	}
	if bad {
		out = append(out, command{prog: line, opaque: true})
	}
	return out
}

// commandFromArgv returns the commands of an argv-style invocation.
func commandFromArgv(argv []string) []command {
	return unwrap(argv, 0)
}

// splitWords splits line into the words of its simple commands. Command
// substitutions inside double quotes are returned as subs, to be parsed on
// their own. bad reports an unterminated quote or substitution.
func splitWords(line string) (segs [][]string, subs []string, bad bool) {
	var (
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
			case r == '$' && i+1 < len(rs) && rs[i+1] == '(':
				end := closingParen(rs, i+2)
				if end < 0 {
					return segs, subs, true
				}
				subs = append(subs, string(rs[i+2:end]))
				word.WriteString(string(rs[i : end+1]))
				i = end
			case r == '`':
				end := closingBacktick(rs, i+1)
				if end < 0 {
					return segs, subs, true
				}
				subs = append(subs, strings.ReplaceAll(string(rs[i+1:end]), "\\`", "`"))
				word.WriteString(string(rs[i : end+1]))
				i = end
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
	return segs, subs, quote != 0
}

// closingParen returns the index of the ) that closes a substitution whose
// body starts at i, or -1.
func closingParen(rs []rune, i int) int {
	depth := 1
	var quote rune
	for ; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			}
		case r == '\\':
			i++
		case quote == '"':
			if r == '"' {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '(':
			depth++
		case r == ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// closingBacktick returns the index of the next unescaped backtick from i, or
// -1.
func closingBacktick(rs []rune, i int) int {
	for ; i < len(rs); i++ {
		switch rs[i] {
		case '\\':
			i++
		case '`':
			return i
		}
	}
	return -1
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ash": true, "ksh": true}

// keywords are the reserved words that open or close compound commands. The
// command after them is the one that runs.
var keywords = map[string]bool{
	"{": true, "}": true, "!": true, "if": true, "then": true, "else": true, "elif": true, "fi": true,
	"while": true, "until": true, "do": true, "done": true, "esac": true,
}

// headers are keywords whose segment holds no command.
var headers = map[string]bool{"for": true, "select": true, "case": true}

// optsWithArg lists, per wrapper, the options that consume the next word.
var optsWithArg = map[string]map[string]bool{
	"env":     {"-u": true, "--unset": true, "-C": true, "--chdir": true},
	"sudo":    {"-u": true, "-g": true, "-h": true, "-p": true, "-C": true, "-D": true, "-R": true, "-T": true, "-U": true},
	"doas":    {"-u": true, "-C": true},
	"nice":    {"-n": true},
	"timeout": {"-s": true, "--signal": true, "-k": true, "--kill-after": true},
	"xargs": {"-a": true, "--arg-file": true, "-d": true, "--delimiter": true, "-E": true, "-I": true, "-L": true,
		"--max-lines": true, "-n": true, "--max-args": true, "-P": true, "--max-procs": true, "-s": true,
		"--max-chars": true, "--process-slot-var": true},
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

// unwrap strips env assignments, keywords and wrapper programs from words and
// returns the commands they run (several, for a `sh -c` script, eval, or
// find -exec).
func unwrap(words []string, depth int) []command {
	for len(words) > 0 {
		w := words[0]
		if isAssignment(w) || keywords[w] {
			words = words[1:]
			continue
		}
		if headers[w] {
			return nil
		}
		if w == "function" {
			words = words[min(2, len(words)):]
			continue
		}
		if w == "" || w != "[" && w != "[[" && strings.ContainsAny(w, "$`*?[ \t\n") {
			return []command{{prog: w, args: words[1:], opaque: true}}
		}
		base := filepath.Base(w)
		switch base {
		case "env":
			for _, a := range words[1:] {
				if strings.HasPrefix(a, "-S") || strings.HasPrefix(a, "--split-string") {
					return []command{{prog: base, args: words[1:], opaque: true}}
				}
			}
			words = skipOptions(words[1:], optsWithArg[base])
			continue
		case "sudo", "doas", "nice":
			words = skipOptions(words[1:], optsWithArg[base])
			continue
		case "command", "builtin", "nohup", "time", "exec", "setsid":
			words = skipOptions(words[1:], nil)
			continue
		case "timeout":
			words = skipOptions(words[1:], optsWithArg[base])
			if len(words) > 0 {
				words = words[1:]
			}
			continue
		case "xargs":
			rest := skipOptions(words[1:], optsWithArg[base])
			if len(rest) == 0 {
				return []command{{prog: "echo"}}
			}
			words = rest
			continue
		case "eval":
			return parseShellDepth(strings.Join(words[1:], " "), depth+1)
		case "find":
			return findCommands(words, depth)
		}
		if shells[base] {
			return shellCommand(base, words, depth)
		}
		return []command{{prog: base, args: words[1:]}}
	}
	return nil
}

// shellCommand returns the commands a shell invocation runs: its -c script,
// or an opaque command when it reads a script from stdin.
func shellCommand(base string, words []string, depth int) []command {
	for i := 1; i < len(words); i++ {
		a := words[i]
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
			if i+1 < len(words) {
				return parseShellDepth(words[i+1], depth+1)
			}
			return []command{{prog: base, args: words[1:], opaque: true}}
		}
		switch {
		case a == "-o" || a == "+o" || a == "-O" || a == "+O":
			i++
		case a == "--":
			if i+1 < len(words) {
				return []command{{prog: base, args: words[1:]}}
			}
			return []command{{prog: base, args: words[1:], opaque: true}}
		case a == "-" || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "s"):
			return []command{{prog: base, args: words[1:], opaque: true}}
		case !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+"):
			return []command{{prog: base, args: words[1:]}}
		}
	}
	return []command{{prog: base, args: words[1:], opaque: true}}
}

// findCommands returns find itself and the commands of its -exec, -execdir,
// -ok and -okdir actions.
func findCommands(words []string, depth int) []command {
	out := []command{{prog: "find", args: words[1:]}}
	for i := 1; i < len(words); i++ {
		switch words[i] {
		case "-exec", "-execdir", "-ok", "-okdir":
		default:
			continue
		}
		j := i + 1
		for j < len(words) && words[j] != ";" && !(words[j] == "+" && words[j-1] == "{}") {
			j++
		}
		out = append(out, unwrap(words[i+1:j], depth)...)
		i = j
	}
	return out
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
