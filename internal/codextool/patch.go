// Package codextool parses Codex tool payloads shared by the Codex adapter and
// the clients' renderers.
package codextool

import (
	"fmt"
	"strings"
)

type PatchOp string

const (
	PatchAdd    PatchOp = "add"
	PatchUpdate PatchOp = "update"
	PatchDelete PatchOp = "delete"
)

// PatchLine is one body line of a patch file: Kind is '+', '-', ' ' (context),
// or '@' for a hunk header whose Text is the optional context after "@@".
type PatchLine struct {
	Kind byte
	Text string
}

type PatchFile struct {
	Op     PatchOp
	Path   string
	MoveTo string
	Lines  []PatchLine
}

// ParsePatch reads a Codex apply_patch input ("*** Begin Patch" … "*** End
// Patch"). ok is false when the input is not a patch or has body lines before
// any file header.
func ParsePatch(input string) (files []PatchFile, ok bool) {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(input, "\r\n", "\n"), "\n"), "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || strings.TrimSpace(lines[i]) != "*** Begin Patch" {
		return nil, false
	}
	for _, ln := range lines[i+1:] {
		t := strings.TrimRight(ln, " ")
		switch {
		case t == "*** End Patch":
			return files, len(files) > 0
		case strings.HasPrefix(t, "*** Add File: "):
			files = append(files, PatchFile{Op: PatchAdd, Path: strings.TrimPrefix(t, "*** Add File: ")})
		case strings.HasPrefix(t, "*** Update File: "):
			files = append(files, PatchFile{Op: PatchUpdate, Path: strings.TrimPrefix(t, "*** Update File: ")})
		case strings.HasPrefix(t, "*** Delete File: "):
			files = append(files, PatchFile{Op: PatchDelete, Path: strings.TrimPrefix(t, "*** Delete File: ")})
		case strings.HasPrefix(t, "*** Move to: "):
			if n := len(files); n > 0 {
				files[n-1].MoveTo = strings.TrimPrefix(t, "*** Move to: ")
			}
		case t == "*** End of File":
			// trailing marker after the last hunk; nothing to show
		case len(files) == 0:
			return nil, false
		case strings.HasPrefix(ln, "@@"):
			files[len(files)-1].Lines = append(files[len(files)-1].Lines, PatchLine{'@', strings.TrimSpace(strings.TrimPrefix(ln, "@@"))})
		case ln == "":
			files[len(files)-1].Lines = append(files[len(files)-1].Lines, PatchLine{' ', ""})
		case ln[0] == '+' || ln[0] == '-' || ln[0] == ' ':
			files[len(files)-1].Lines = append(files[len(files)-1].Lines, PatchLine{ln[0], ln[1:]})
		default:
			files[len(files)-1].Lines = append(files[len(files)-1].Lines, PatchLine{' ', ln})
		}
	}
	return files, len(files) > 0
}

// PatchSummary is a one-line summary: the single file's path (its move
// destination when moved), or "N files".
func PatchSummary(files []PatchFile) string {
	switch len(files) {
	case 0:
		return ""
	case 1:
		if files[0].MoveTo != "" {
			return files[0].MoveTo
		}
		return files[0].Path
	}
	return fmt.Sprintf("%d files", len(files))
}
