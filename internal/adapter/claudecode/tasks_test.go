package claudecode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func writeTask(t *testing.T, dir, id, status string) string {
	t.Helper()
	p := filepath.Join(dir, id+".json")
	body := `{"id":"` + id + `","subject":"s` + id + `","activeForm":"a` + id + `","status":"` + status + `","blocks":[],"blockedBy":[]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// setup points HOME at a temp dir and returns the session-<short> task dir for a
// synthetic transcript (short segment "abcd1234").
func setup(t *testing.T) (transcriptPath, taskDir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude")
	transcriptPath = filepath.Join(claude, "projects", "-proj", "abcd1234-1111-2222-3333-444455556666.jsonl")
	taskDir = filepath.Join(claude, "tasks", "session-abcd1234")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return transcriptPath, taskDir
}

func TestReadTasks_SortedSkipsNonJSON(t *testing.T) {
	tp, dir := setup(t)
	// Non-contiguous ids + noise files that must be ignored.
	writeTask(t, dir, "14", "completed")
	writeTask(t, dir, "2", "pending")
	writeTask(t, dir, "10", "in_progress")
	if err := os.WriteFile(filepath.Join(dir, ".lock"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".highwatermark"), []byte("14"), 0o644); err != nil {
		t.Fatal(err)
	}

	tasks, err := ReadTasks(nil, tp)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, tk := range tasks {
		got = append(got, tk.ID)
	}
	want := []string{"2", "10", "14"} // numeric sort, not lexical
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order: got %v, want %v", got, want)
		}
	}
	if tasks[1].Status != "in_progress" || tasks[1].ActiveForm != "a10" {
		t.Fatalf("field mapping wrong: %+v", tasks[1])
	}
}

func TestReadTasks_FullUUIDFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude")
	tp := filepath.Join(claude, "projects", "-proj", "abcd1234-1111-2222-3333-444455556666.jsonl")
	dir := filepath.Join(claude, "tasks", "abcd1234-1111-2222-3333-444455556666")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTask(t, dir, "1", "pending")
	writeTask(t, dir, "2", "completed")

	tasks, err := ReadTasks(nil, tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != "1" || tasks[1].ID != "2" {
		t.Fatalf("full-uuid fallback: got %+v", tasks)
	}
}

func TestReadTasks_FullUUIDWinsOverSessionShort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude")
	tp := filepath.Join(claude, "projects", "-proj", "abcd1234-1111-2222-3333-444455556666.jsonl")
	fullUUID := filepath.Join(claude, "tasks", "abcd1234-1111-2222-3333-444455556666")
	sessionShort := filepath.Join(claude, "tasks", "session-abcd1234")
	for _, d := range []string{fullUUID, sessionShort} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTask(t, fullUUID, "9", "completed")
	writeTask(t, sessionShort, "1", "in_progress")

	tasks, err := ReadTasks(nil, tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "9" {
		t.Fatalf("full-uuid should win: got %+v", tasks)
	}
}

func TestReadTasks_UsesExplicitSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude")
	// Filename is the current (resumed) id; the real dir is keyed by the root
	// session_id, which differs after a resume.
	tp := filepath.Join(claude, "projects", "-proj", "current1-1111-2222-3333-444455556666.jsonl")
	rootID := "root2222-aaaa-bbbb-cccc-ddddeeeeffff"
	dir := filepath.Join(claude, "tasks", rootID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTask(t, dir, "5", "in_progress")

	tasks, err := ReadTasks([]string{rootID}, tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "5" {
		t.Fatalf("explicit session id should key the dir: got %+v", tasks)
	}
}

// TestReadTasks_FilenameFallbackWhenSessionIDMisses covers the real corpus case
// where the transcript's task lines carry a session_id with no dir, while the
// board lives under the transcript filename. The read must fall back to the
// filename even though a non-empty sessionID was supplied.
func TestReadTasks_FilenameFallbackWhenSessionIDMisses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude")
	fname := "90ab9f31-ff3c-431a-a5d5-8d78b615d495"
	tp := filepath.Join(claude, "projects", "-proj", fname+".jsonl")
	dir := filepath.Join(claude, "tasks", fname)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTask(t, dir, "1", "in_progress")

	// The task lines carried this id, but no such dir exists.
	tasks, err := ReadTasks([]string{"9e19498a-7fd1-4d8a-a55e-cde668a0a3e1"}, tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "1" {
		t.Fatalf("should fall back to the filename dir: got %+v", tasks)
	}
}

// TestReadTasks_EarlierCandidateWinsWhenBothPopulated locks the tie-break: when
// two candidate dirs both hold tasks, the earlier candidate (the root session_id,
// which TasksCandidates orders first) wins over a later one (the task-line id), so
// a stale board cannot mask the live one.
func TestReadTasks_EarlierCandidateWinsWhenBothPopulated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude")
	tp := filepath.Join(claude, "projects", "-proj", "current1-1111-2222-3333-444455556666.jsonl")
	rootID := "root2222-aaaa-bbbb-cccc-ddddeeeeffff"
	taskLineID := "task3333-aaaa-bbbb-cccc-ddddeeeeffff"
	rootDir := filepath.Join(claude, "tasks", rootID)
	taskDir := filepath.Join(claude, "tasks", taskLineID)
	for _, d := range []string{rootDir, taskDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTask(t, rootDir, "1", "in_progress")
	writeTask(t, taskDir, "9", "completed")

	tasks, err := ReadTasks([]string{rootID, taskLineID}, tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "1" {
		t.Fatalf("earlier candidate (root) should win: got %+v", tasks)
	}
}

// TestReadTasks_SkipsEmptyEarlierDir locks the first-populated (not first-existing)
// rule: an earlier candidate dir that exists but holds no tasks must not mask a
// populated later one.
func TestReadTasks_SkipsEmptyEarlierDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude")
	tp := filepath.Join(claude, "projects", "-proj", "current1-1111-2222-3333-444455556666.jsonl")
	rootID := "root2222-aaaa-bbbb-cccc-ddddeeeeffff"
	taskLineID := "task3333-aaaa-bbbb-cccc-ddddeeeeffff"
	rootDir := filepath.Join(claude, "tasks", rootID)
	taskDir := filepath.Join(claude, "tasks", taskLineID)
	for _, d := range []string{rootDir, taskDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// rootDir exists but is empty; the live board sits under the later candidate.
	writeTask(t, taskDir, "9", "completed")

	tasks, err := ReadTasks([]string{rootID, taskLineID}, tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "9" {
		t.Fatalf("empty earlier dir must not mask a populated later one: got %+v", tasks)
	}
}

func TestCandidateDirs(t *testing.T) {
	root := "root2222-aaaa-bbbb-cccc-ddddeeeeffff"
	base := "abcd1234-1111-2222-3333-444455556666"
	tasksDir := func(key string) string { return filepath.Join("/home", "tasks", key) }

	tests := []struct {
		name string
		keys []string
		base string
		want []string
	}{
		{
			name: "root leads, base appended last, dupes dropped",
			keys: []string{root, root},
			base: base,
			want: []string{tasksDir(root), tasksDir("session-root2222"), tasksDir(base), tasksDir("session-abcd1234")},
		},
		{
			name: "key equal to base is deduped",
			keys: []string{base},
			base: base,
			want: []string{tasksDir(base), tasksDir("session-abcd1234")},
		},
		{
			name: "empty keys skipped",
			keys: []string{"", root, ""},
			base: "",
			want: []string{tasksDir(root), tasksDir("session-root2222")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := candidateDirs("/home", "tasks", tt.keys, tt.base); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("candidateDirs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadTasks_MissingDirIsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tp := filepath.Join(home, ".claude", "projects", "-proj", "deadbeef-0000.jsonl")
	tasks, err := ReadTasks(nil, tp)
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("want empty, got %v", tasks)
	}
}

func TestTaskActivityCount(t *testing.T) {
	tool := func(name, result string) transcript.Item {
		return transcript.Item{Kind: transcript.ItemTool, ToolName: name, Result: result}
	}
	teammateMsg := func(id string) transcript.Item {
		return transcript.Item{
			Kind:      transcript.ItemSubagent,
			Subagents: []transcript.Subagent{{Name: id, IsTeammate: true}},
		}
	}
	chunks := []transcript.Chunk{
		{Kind: transcript.ChunkAI, Items: []transcript.Item{
			tool("Bash", "ok"),               // not a task tool
			tool("TaskCreate", "created #1"), // counts (lead)
			tool("TaskUpdate", ""),           // in-flight, no result yet — excluded
		}},
		{Kind: transcript.ChunkAI, Items: []transcript.Item{
			tool("TaskStop", "stopped #2"),                  // counts (lead)
			{Kind: transcript.ItemText, Text: "TaskCreate"}, // text, not a tool item
			teammateMsg("daneel"),                           // counts (teammate reported back)
			{Kind: transcript.ItemSubagent},                 // a spawn, not a teammate — excluded
		}},
	}
	if got, hasTool := TaskActivityCount(chunks); got != 3 || !hasTool {
		t.Fatalf("TaskActivityCount = (%d, %v), want (3, true)", got, hasTool)
	}
	if got, hasTool := TaskActivityCount(nil); got != 0 || hasTool {
		t.Fatalf("TaskActivityCount(nil) = (%d, %v), want (0, false)", got, hasTool)
	}
	// Teammate message with no task tool: counts, but hasTaskTool is false so the
	// caller must confirm tasks exist before firing.
	tmOnly := []transcript.Chunk{{Kind: transcript.ChunkAI, Items: []transcript.Item{teammateMsg("dors")}}}
	if got, hasTool := TaskActivityCount(tmOnly); got != 1 || hasTool {
		t.Fatalf("TaskActivityCount(teammate-only) = (%d, %v), want (1, false)", got, hasTool)
	}
}
