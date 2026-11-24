package parser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// WorkflowLaunch is one Workflow tool invocation found in a session transcript.
type WorkflowLaunch struct {
	ToolID     string    // Workflow tool_use id
	RunID      string    // toolUseResult.runId; "" until the tool result is read, or for remote runs
	Name       string    // toolUseResult.workflowName, else ""
	Remote     bool      // toolUseResult.taskType == "remote_agent"
	LaunchedAt time.Time // timestamp of the tool_use line
	End        string    // "" while no end record; else mapped status: "completed" | "failed" | "stopped" | raw
}

// WorkflowScanner incrementally scans a session JSONL for Workflow launches
// and their end records. Scan reads only complete lines appended since the
// previous call.
type WorkflowScanner struct {
	path     string
	offset   int64
	launches map[string]*WorkflowLaunch
	order    []string
	resolved map[string]bool // tool ids whose tool_result has been read
}

func NewWorkflowScanner(path string) *WorkflowScanner {
	s := &WorkflowScanner{path: path}
	s.reset()
	return s
}

func (s *WorkflowScanner) reset() {
	s.offset = 0
	s.launches = map[string]*WorkflowLaunch{}
	s.order = nil
	s.resolved = map[string]bool{}
}

// Scan consumes complete lines appended since the last call. If the file
// shrank, state is discarded and the file is rescanned from the start.
func (s *WorkflowScanner) Scan() error {
	f, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < s.offset {
		s.reset()
	}
	if _, err := f.Seek(s.offset, io.SeekStart); err != nil {
		return err
	}
	// Read line by line so memory stays bounded by the longest line; an
	// oversized line is skipped, and a trailing partial line is left for the
	// next call.
	r := bufio.NewReaderSize(f, initialBufSize)
	var line []byte
	var n int64
	skip := false
	for {
		chunk, err := r.ReadSlice('\n')
		n += int64(len(chunk))
		switch err {
		case nil:
			if !skip {
				if len(line) > 0 {
					chunk = append(line, chunk...)
				}
				s.scanLine(chunk[:len(chunk)-1])
			}
			s.offset += n
			line, n, skip = line[:0], 0, false
		case bufio.ErrBufferFull:
			if !skip {
				line = append(line, chunk...)
				if len(line) > maxLineSize {
					line, skip = line[:0], true
				}
			}
		case io.EOF:
			return nil
		default:
			return err
		}
	}
}

// Launches returns a copy of all launches in launch order.
func (s *WorkflowScanner) Launches() []WorkflowLaunch {
	out := make([]WorkflowLaunch, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, *s.launches[id])
	}
	return out
}

func (s *WorkflowScanner) Launch(toolID string) (WorkflowLaunch, bool) {
	l, ok := s.launches[toolID]
	if !ok {
		return WorkflowLaunch{}, false
	}
	return *l, true
}

// MapWorkflowEnd maps a task-notification status to a workflow end status.
func MapWorkflowEnd(status string) string {
	if status == "killed" {
		return "stopped"
	}
	return status
}

type workflowLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	// A string on errors and rejections, so decoded separately.
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type workflowToolUseResult struct {
	RunID        string `json:"runId"`
	WorkflowName string `json:"workflowName"`
	TaskType     string `json:"taskType"`
	Status       string `json:"status"`
}

var (
	wfMarkerWorkflow   = []byte(`"Workflow"`)
	wfMarkerNotify     = []byte(taskNotificationTag)
	wfMarkerNotifyEsc  = []byte(`\u003ctask-notification`)
	wfMarkerToolResult = []byte(`"tool_result"`)
)

// maybeRelevant is a cheap prefilter run before JSON decoding. A tool_result
// line matters only when it names a pending launch.
func (s *WorkflowScanner) maybeRelevant(line []byte) bool {
	if bytes.Contains(line, wfMarkerWorkflow) ||
		bytes.Contains(line, wfMarkerNotify) || bytes.Contains(line, wfMarkerNotifyEsc) {
		return true
	}
	if len(s.resolved) < len(s.launches) && bytes.Contains(line, wfMarkerToolResult) {
		for id := range s.launches {
			if !s.resolved[id] && bytes.Contains(line, []byte(id)) {
				return true
			}
		}
	}
	return false
}

func (s *WorkflowScanner) scanLine(line []byte) {
	if !s.maybeRelevant(line) {
		return
	}
	var wl workflowLine
	if json.Unmarshal(line, &wl) != nil {
		return
	}
	c := decodeContent(wl.Message.Content)
	switch wl.Type {
	case "assistant":
		s.scanToolUses(c.blocks, wl.Timestamp)
	case "user":
		s.scanToolResults(c.blocks, wl.ToolUseResult)
		s.scanNotifications(c.text())
	}
}

func (s *WorkflowScanner) scanToolUses(blocks []contentBlockJSON, timestamp string) {
	for _, b := range blocks {
		if b.Type != "tool_use" || b.Name != "Workflow" || b.ID == "" {
			continue
		}
		if _, dup := s.launches[b.ID]; dup {
			continue
		}
		s.launches[b.ID] = &WorkflowLaunch{ToolID: b.ID, LaunchedAt: parseTimestamp(timestamp)}
		s.order = append(s.order, b.ID)
	}
}

func (s *WorkflowScanner) scanToolResults(blocks []contentBlockJSON, toolUseResult json.RawMessage) {
	for _, b := range blocks {
		l, ok := s.launches[b.ToolUseID]
		if b.Type != "tool_result" || !ok || s.resolved[b.ToolUseID] {
			continue
		}
		s.resolved[b.ToolUseID] = true
		if b.IsError {
			if l.End == "" {
				l.End = "failed"
			}
			continue
		}
		var r workflowToolUseResult
		_ = json.Unmarshal(toolUseResult, &r)
		l.RunID = r.RunID
		l.Name = r.WorkflowName
		l.Remote = r.TaskType == "remote_agent"
		// Without a run id or a pending status the tool finished in the
		// result itself, and no end record will follow.
		if l.End == "" && l.RunID == "" && !l.Remote {
			switch r.Status {
			case "async_launched", "running", "pending":
			case "":
				l.End = "completed"
			default:
				l.End = MapWorkflowEnd(r.Status)
			}
		}
	}
}

// scanNotifications ends launches named by the task notifications in text,
// which may batch several.
func (s *WorkflowScanner) scanNotifications(text string) {
	if !strings.HasPrefix(strings.TrimSpace(text), taskNotificationTag) {
		return
	}
	for _, m := range reTaskNotification.FindAllStringSubmatch(text, -1) {
		idm := reTaskNotifyToolUseID.FindStringSubmatch(m[1])
		stm := reTaskNotifyStatus.FindStringSubmatch(m[1])
		if idm == nil || stm == nil {
			continue
		}
		l, ok := s.launches[strings.TrimSpace(idm[1])]
		if !ok || l.End != "" {
			continue
		}
		l.End = MapWorkflowEnd(strings.TrimSpace(stm[1]))
	}
}
