package codex

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// rolloutItem is the subset of an event_msg item_completed item argus uses: the
// commands a code-mode "exec" script runs are recorded as CommandExecution items.
type rolloutItem struct {
	Type             string   `json:"type"`
	Source           string   `json:"source"`
	ID               string   `json:"id"`
	Command          []string `json:"command"`
	Cwd              string   `json:"cwd"`
	AggregatedOutput string   `json:"aggregated_output"`
	ExitCode         *int     `json:"exit_code"`
	Duration         *struct {
		Secs  int64 `json:"secs"`
		Nanos int64 `json:"nanos"`
	} `json:"duration"`
}

// nestedCommandEntry renders a command run inside a code-mode script as an
// exec_command row, so it gets the same summary and detail view.
func nestedCommandEntry(it *rolloutItem, ts string) transcript.Entry {
	cmd := strings.Join(it.Command, " ")
	if n := len(it.Command); n >= 3 && it.Command[n-2] == "-lc" {
		cmd = it.Command[n-1] // the shell wrapper's script, as the model wrote it
	}
	in, _ := json.Marshal(map[string]string{"cmd": cmd, "workdir": strings.TrimPrefix(it.Cwd, "file://")})
	var head strings.Builder
	if it.ExitCode != nil {
		fmt.Fprintf(&head, "Process exited with code %d\n", *it.ExitCode)
	}
	if it.Duration != nil {
		fmt.Fprintf(&head, "Wall time: %.4f seconds\n", float64(it.Duration.Secs)+float64(it.Duration.Nanos)/1e9)
	}
	return transcript.Entry{
		Kind:          transcript.EntryTool,
		Timestamp:     ts,
		ToolName:      "exec_command",
		ToolID:        it.ID,
		ToolInput:     string(in),
		InputPreview:  previewLine(cmd),
		Result:        head.String() + "Output:\n" + it.AggregatedOutput,
		ResultIsError: it.ExitCode != nil && *it.ExitCode != 0,
	}
}

var (
	codeModeToolRe = regexp.MustCompile(`tools\.([A-Za-z0-9_]+)\s*\(`)
	codeModeCmdRe  = regexp.MustCompile("cmd\\s*:\\s*(\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`)")
)

// codeModePreview summarizes a code-mode script by the tools it calls
// ("exec_command: <cmd>" for commands), else its first non-empty line.
func codeModePreview(js string) string {
	preview, _ := scanCodeMode(js)
	return preview
}

// scanCodeMode returns codeModePreview's summary and whether every tool the
// script calls is exec_command, the one tool Codex records as its own item.
func scanCodeMode(js string) (preview string, onlyCommands bool) {
	locs := codeModeToolRe.FindAllStringSubmatchIndex(js, -1)
	if len(locs) == 0 {
		for _, ln := range strings.Split(js, "\n") {
			if s := strings.TrimSpace(ln); s != "" {
				return s, true
			}
		}
		return "", true
	}
	onlyCommands = true
	parts := make([]string, 0, len(locs))
	for i, l := range locs {
		name := js[l[2]:l[3]]
		part := name
		if name != "exec_command" {
			onlyCommands = false
		} else {
			end := len(js)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			if m := codeModeCmdRe.FindStringSubmatch(js[l[1]:end]); m != nil {
				part += ": " + previewLine(unquoteJS(m[1]))
			}
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", "), onlyCommands
}

func unquoteJS(s string) string {
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return strings.ReplaceAll(s[1:len(s)-1], `\'`, `'`)
}

// execCall tracks an open code-mode exec call. slot is the index of its row
// while a recorded command may still take it over (the script calls only
// exec_command), else -1; replaced is set once a command did.
type execCall struct {
	id, input string
	slot      int
	replaced  bool
}
