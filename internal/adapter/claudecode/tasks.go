package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// diskTask matches the on-disk JSON (camelCase) of one ~/.claude/tasks file.
type diskTask struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	ActiveForm  string   `json:"activeForm"`
	Status      string   `json:"status"`
	Blocks      []string `json:"blocks"`
	BlockedBy   []string `json:"blockedBy"`
}

// taskDirs returns the two candidate task directories for a session. Newer
// Claude Code keys them by session-<short> (first UUID segment); older sessions
// used the full transcript UUID. primary is the session-<short> path, fallback
// the full-uuid one. Empty strings when ~/.claude cannot be located.
func taskDirs(transcriptPath string) (primary, fallback string) {
	home := claudeHome()
	if home == "" {
		return "", ""
	}
	base := strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl")
	return filepath.Join(home, "tasks", "session-"+sessionShort(base)), filepath.Join(home, "tasks", base)
}

// sessionShort is the session-<short> key Claude Code uses for a transcript's
// tasks/ and teams/ dirs: the first UUID segment of the transcript filename.
func sessionShort(base string) string {
	if i := strings.IndexByte(base, '-'); i > 0 {
		return base[:i]
	}
	return base
}

// ReadTasks returns a session's current task list, ordered by numeric id. A
// missing/empty task dir yields an empty slice, not an error.
func ReadTasks(transcriptPath string) ([]api.Task, error) {
	primary, fallback := taskDirs(transcriptPath)
	if primary == "" {
		return nil, nil
	}
	tasks, err := readTaskDir(primary)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		if fb, ferr := readTaskDir(fallback); ferr == nil {
			return fb, nil
		}
	}
	return tasks, nil
}

func readTaskDir(dir string) ([]api.Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []api.Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue // skip .lock, .highwatermark, subdirs
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // transiently locked/removed; skip
		}
		var dt diskTask
		if json.Unmarshal(b, &dt) != nil {
			continue
		}
		tasks = append(tasks, api.Task{
			ID:          dt.ID,
			Subject:     dt.Subject,
			Description: dt.Description,
			ActiveForm:  dt.ActiveForm,
			Status:      dt.Status,
			Blocks:      dt.Blocks,
			BlockedBy:   dt.BlockedBy,
		})
	}
	sort.Slice(tasks, func(i, j int) bool { return taskIDLess(tasks[i].ID, tasks[j].ID) })
	return tasks, nil
}

func taskIDLess(a, b string) bool {
	if ai, aerr := strconv.Atoi(a); aerr == nil {
		if bi, berr := strconv.Atoi(b); berr == nil {
			return ai < bi
		}
	}
	return a < b
}

// taskMutatingTools are the tool calls that create or change tasks; each is a
// distinct item in the transcript, so their running count only grows.
var taskMutatingTools = map[string]bool{
	"TaskCreate": true,
	"TaskUpdate": true,
	"TaskStop":   true,
}

// TaskActivityCount counts signals in the main transcript that a session's task
// list may have changed. The poller fires tasks.changed when this grows, so live
// updates key off the ai chunk (what the user saw), not a disk poll. Two signals:
//
//   - Completed task-mutating tool calls (TaskCreate/TaskUpdate/TaskStop). A
//     result means the tool finished, so the task file is already written and the
//     client's follow-up read is fresh. This covers the lead, which runs these
//     directly.
//   - Teammate messages. In a multi-agent team, teammates run their TaskUpdate
//     calls in their own (untailed) subagent transcripts, but they report back to
//     the lead via <teammate-message> entries that DO land in the main transcript
//     — and those arrive after the teammate's disk write, so re-pulling on one
//     reads fresh state. This is a proxy (a teammate could change a task without
//     messaging), but the client re-pulls the whole list, so it converges; open
//     and pull-to-refresh cover any straggler.
//
// The count only grows, so a rise means new activity to push. hasTaskTool
// reports whether any task-mutating tool call appears here at all: it proves
// tasks exist for this session without a disk hit, so the poller can gate
// teammate-message-only activity (which many task-less teams also produce) on
// tasks actually existing.
func TaskActivityCount(chunks []transcript.Chunk) (count int, hasTaskTool bool) {
	for i := range chunks {
		for j := range chunks[i].Items {
			it := chunks[i].Items[j]
			switch {
			case it.Kind == transcript.ItemTool && taskMutatingTools[it.ToolName] &&
				(it.Result != "" || it.ResultIsError):
				count++
				hasTaskTool = true
			case it.IsTeammate():
				count++
			}
		}
	}
	return count, hasTaskTool
}
