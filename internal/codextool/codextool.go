package codextool

import "strings"

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
