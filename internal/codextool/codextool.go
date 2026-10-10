package codextool

import (
	"strings"
	"time"
)

// EncryptedPrefix starts the Fernet tokens Codex sends for encrypted content.
const EncryptedPrefix = "gAAAAA"

// UserNotePrefix marks the note entry in a request_user_input answer.
const UserNotePrefix = "user_note: "

// SplitAnswer separates a request_user_input answer's selected label from its
// note entry.
func SplitAnswer(vals []string) (label, note string) {
	for _, v := range vals {
		if n, ok := strings.CutPrefix(v, UserNotePrefix); ok {
			note = n
		} else if label == "" {
			label = v
		}
	}
	return label, note
}

// MsDuration renders a millisecond count as a short duration ("10s"); empty for
// zero.
func MsDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return (time.Duration(ms) * time.Millisecond).String()
}
