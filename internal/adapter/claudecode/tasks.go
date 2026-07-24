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

// diskTask is the on-disk JSON of one task file (camelCase, vs api.Task's snake_case).
type diskTask struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	ActiveForm  string   `json:"activeForm"`
	Status      string   `json:"status"`
	Blocks      []string `json:"blocks"`
	BlockedBy   []string `json:"blockedBy"`
}

// taskDirs returns candidate task dirs, most-likely first. The session-<short>
// dir only exists under CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS.
func taskDirs(transcriptPath string) []string {
	home := claudeHome()
	if home == "" {
		return nil
	}
	base := strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl")
	return []string{
		filepath.Join(home, "tasks", base),
		filepath.Join(home, "tasks", "session-"+sessionShort(base)),
	}
}

func sessionShort(base string) string {
	if i := strings.IndexByte(base, '-'); i > 0 {
		return base[:i]
	}
	return base
}

// ReadTasks returns a session's task list ordered by numeric id, from the first
// candidate dir that has tasks. Missing dirs are not errors; a read error
// surfaces only when no candidate yielded tasks.
func ReadTasks(transcriptPath string) ([]api.Task, error) {
	var firstErr error
	for _, dir := range taskDirs(transcriptPath) {
		tasks, err := readTaskDir(dir)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(tasks) > 0 {
			return tasks, nil
		}
	}
	return nil, firstErr
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

var taskMutatingTools = map[string]bool{
	"TaskCreate": true,
	"TaskUpdate": true,
	"TaskStop":   true,
}

// TaskActivityCount counts signals in the main transcript that a session's task
// list may have changed, so live updates key off the folded chunks (what the
// user saw) rather than a disk poll. Two signals:
//
//   - Completed task-mutating tool calls (TaskCreate/TaskUpdate/TaskStop): a
//     result means the file is already written, so the client's follow-up read
//     is fresh. Covers the lead, which runs these directly.
//   - Teammate messages: teammates run TaskUpdate in their own (untailed)
//     subagent transcripts but report back via <teammate-message> entries that
//     DO land in the main one, after the disk write — so a re-pull reads fresh
//     state. A proxy (a teammate could change a task without messaging), but the
//     client re-pulls the whole list so it converges; open and pull-to-refresh
//     cover any straggler.
//
// Each counted item is append-only in the transcript, so count only grows: a
// rise means new activity to push. hasTaskTool reports whether any task-mutating
// tool call is present — proof that tasks exist for this session without a disk
// hit, letting the poller gate teammate-only activity (task-less teams also
// produce it) on tasks actually existing.
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
