package parser_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

func asstToolLine(uuid, sessionID, toolID, toolName, input string) string {
	return `{"uuid":"` + uuid + `","type":"assistant","session_id":"` + sessionID +
		`","message":{"role":"assistant","model":"claude","content":[{"type":"tool_use","id":"` +
		toolID + `","name":"` + toolName + `","input":` + input + `}]}}`
}

func userLine(uuid, sessionID, text string) string {
	return `{"uuid":"` + uuid + `","type":"user","session_id":"` + sessionID +
		`","message":{"role":"user","content":"` + text + `"}}`
}

func writeTranscript(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	data := ""
	for _, l := range lines {
		data += l + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveSessionDirKeys_PerRelevantLine(t *testing.T) {
	path := writeTranscript(t, []string{
		asstToolLine("u1", "s1", "t1", "TaskCreate", `{"subject":"x"}`),
		asstToolLine("u2", "s2", "t2", "TeamCreate", `{"team_name":"docs"}`),
		asstToolLine("u3", "s3", "t3", "Read", `{"file_path":"/x"}`),
	})
	chunks, err := parser.ReadSession(path)
	if err != nil {
		t.Fatal(err)
	}

	keys := parser.ResolveSessionDirKeys(chunks)
	if keys.Tasks != "s1" {
		t.Errorf("Tasks = %q, want %q (session_id of the TaskCreate line)", keys.Tasks, "s1")
	}
	if keys.Teams != "s2" {
		t.Errorf("Teams = %q, want %q (session_id of the TeamCreate line)", keys.Teams, "s2")
	}
	if keys.Last != "s3" {
		t.Errorf("Last = %q, want %q (session_id of the last chunk)", keys.Last, "s3")
	}
}

func TestResolveSessionDirKeys_FirstAndLast(t *testing.T) {
	// First and Last bound the session_ids seen; separate user chunks keep them
	// distinct so both become tasks-dir candidates.
	path := writeTranscript(t, []string{
		userLine("u1", "root-id", "hi"),
		userLine("u2", "later-id", "again"),
	})
	chunks, err := parser.ReadSession(path)
	if err != nil {
		t.Fatal(err)
	}

	keys := parser.ResolveSessionDirKeys(chunks)
	if keys.First != "root-id" {
		t.Errorf("First = %q, want %q", keys.First, "root-id")
	}
	if keys.Last != "later-id" {
		t.Errorf("Last = %q, want %q", keys.Last, "later-id")
	}
	if got := keys.TasksCandidates(); len(got) != 2 || got[0] != "root-id" || got[1] != "later-id" {
		t.Errorf("TasksCandidates = %v, want [root-id later-id]", got)
	}
}

func TestResolveSessionDirKeys_LastTaskLineWins(t *testing.T) {
	path := writeTranscript(t, []string{
		asstToolLine("u1", "s1", "t1", "TaskCreate", `{"subject":"x"}`),
		asstToolLine("u2", "s4", "t2", "TaskUpdate", `{"taskId":"1"}`),
	})
	chunks, err := parser.ReadSession(path)
	if err != nil {
		t.Fatal(err)
	}

	if keys := parser.ResolveSessionDirKeys(chunks); keys.Tasks != "s4" {
		t.Errorf("Tasks = %q, want %q (last task-mutating line)", keys.Tasks, "s4")
	}
}

func TestResolveSessionDirKeys_RootLeadsTaskLineID(t *testing.T) {
	// A resume chain: the root session_id differs from the id on the task-writing
	// line. The root must lead the candidates so a stale task-line dir cannot mask
	// the live root-keyed board.
	path := writeTranscript(t, []string{
		userLine("u0", "root-id", "hi"),
		asstToolLine("u1", "task-id", "t1", "TaskCreate", `{"subject":"x"}`),
	})
	chunks, err := parser.ReadSession(path)
	if err != nil {
		t.Fatal(err)
	}

	keys := parser.ResolveSessionDirKeys(chunks)
	if keys.First != "root-id" || keys.Tasks != "task-id" {
		t.Fatalf("keys = %+v, want First=root-id Tasks=task-id", keys)
	}
	if got := keys.TasksCandidates(); len(got) != 2 || got[0] != "root-id" || got[1] != "task-id" {
		t.Errorf("TasksCandidates = %v, want [root-id task-id] (root leads)", got)
	}
}

func TestResolveSessionDirKeys_NoSessionID(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"uuid":"u1","type":"assistant","message":{"role":"assistant","model":"claude","content":[{"type":"tool_use","id":"t1","name":"TaskCreate","input":{"subject":"x"}}]}}`,
	})
	chunks, err := parser.ReadSession(path)
	if err != nil {
		t.Fatal(err)
	}

	keys := parser.ResolveSessionDirKeys(chunks)
	if keys.Tasks != "" || keys.Teams != "" || keys.First != "" || keys.Last != "" {
		t.Errorf("expected all-empty keys without session_id, got %+v", keys)
	}
	if got := keys.TasksCandidates(); len(got) != 0 {
		t.Errorf("TasksCandidates = %v, want empty", got)
	}
}

func TestResolveSessionDirKeys_CandidatesFallBackToRoot(t *testing.T) {
	// A task-only session has no team line, so the teams candidates fall back to
	// the root/last session_id.
	path := writeTranscript(t, []string{
		asstToolLine("u1", "s1", "t1", "TaskCreate", `{"subject":"x"}`),
	})
	chunks, err := parser.ReadSession(path)
	if err != nil {
		t.Fatal(err)
	}

	keys := parser.ResolveSessionDirKeys(chunks)
	if got := keys.TasksCandidates(); len(got) != 1 || got[0] != "s1" {
		t.Errorf("TasksCandidates = %v, want [s1]", got)
	}
	if got := keys.TeamsCandidates(); len(got) != 1 || got[0] != "s1" {
		t.Errorf("TeamsCandidates = %v, want [s1] (fallback to root)", got)
	}
}

func TestResolveSessionDirKeys_UserOnlyTranscript(t *testing.T) {
	// Right after a resume the transcript can hold a user prompt but no assistant
	// line yet. The candidates must still come from the user line so the read
	// resolves to the root session_id instead of only the (new) filename.
	path := writeTranscript(t, []string{
		userLine("u1", "root-x", "do it"),
	})
	chunks, err := parser.ReadSession(path)
	if err != nil {
		t.Fatal(err)
	}

	keys := parser.ResolveSessionDirKeys(chunks)
	if keys.First != "root-x" || keys.Last != "root-x" {
		t.Errorf("First/Last = %q/%q, want root-x (from the user line)", keys.First, keys.Last)
	}
	if got := keys.TasksCandidates(); len(got) != 1 || got[0] != "root-x" {
		t.Errorf("TasksCandidates = %v, want [root-x]", got)
	}
}

func TestResolveSessionDirKeys_TeamTaskCountsAsTeam(t *testing.T) {
	path := writeTranscript(t, []string{
		asstToolLine("u1", "s7", "t1", "Task", `{"team_name":"docs","name":"writer"}`),
	})
	chunks, err := parser.ReadSession(path)
	if err != nil {
		t.Fatal(err)
	}

	if keys := parser.ResolveSessionDirKeys(chunks); keys.Teams != "s7" {
		t.Errorf("Teams = %q, want %q (team member spawn line)", keys.Teams, "s7")
	}
}
