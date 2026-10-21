package adapter

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mention is a trigger-prefixed name in prompt text, such as $skill or @skill.
type Mention struct {
	At   int // byte offset of the trigger
	Name string
}

// Mentions returns every trigger-prefixed name that starts text or follows
// whitespace. A name starts with a letter, so "$5" is not a mention.
func Mentions(text string, trigger rune) []Mention {
	var out []Mention
	prevSpace := true
	for i, r := range text {
		if r == trigger && prevSpace {
			rest := text[i+len(string(trigger)):]
			end := strings.IndexFunc(rest, notMentionRune)
			if end < 0 {
				end = len(rest)
			}
			if first, _ := utf8.DecodeRuneInString(rest); end > 0 && unicode.IsLetter(first) {
				out = append(out, Mention{At: i, Name: rest[:end]})
			}
		}
		prevSpace = unicode.IsSpace(r)
	}
	return out
}

// IsMentionName reports whether Mentions can find name after a trigger.
func IsMentionName(name string) bool {
	first, _ := utf8.DecodeRuneInString(name)
	return unicode.IsLetter(first) && strings.IndexFunc(name, notMentionRune) < 0
}

func notMentionRune(r rune) bool {
	return r != '_' && r != '-' && r != ':' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
}
