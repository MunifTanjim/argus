package claudecode

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// parentJSONL is a minimal assistant entry that spawns one team member via Task.
const parentJSONL = `{"uuid":"a1","type":"assistant","timestamp":"2025-06-15T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Task","input":{"subagent_type":"general-purpose","description":"Do work","team_name":"myteam","name":"worker"}}],"model":"claude-sonnet-4-20250514","stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`

// memberJSONL has teamName+agentName in first line (required by ReadTeamSessionMeta)
// and an assistant entry so readSubagentSession returns a non-empty chunk slice.
const memberJSONL = `{"uuid":"m1","type":"user","teamName":"myteam","agentName":"worker","timestamp":"2025-06-15T10:00:01Z","message":{"role":"user","content":"Do work"}}
{"uuid":"m2","type":"assistant","timestamp":"2025-06-15T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":50,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`

func TestCollectSessionFiles(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, "projects", "-proj")
	uuid := "bd9d953d-3545-4c85-91c2-049ea8c71743"
	short := "bd9d953d"

	mkfile := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	main := filepath.Join(projects, uuid+".jsonl")
	mkfile(main)
	mkfile(filepath.Join(projects, uuid, "subagents", "agent-a.jsonl"))
	mkfile(filepath.Join(projects, uuid, "subagents", "agent-a.meta.json"))
	mkfile(filepath.Join(home, "tasks", uuid, "1.json"))
	mkfile(filepath.Join(home, "teams", uuid, "config.json"))
	mkfile(filepath.Join(home, "tasks", "session-"+short, "2.json"))
	mkfile(filepath.Join(home, "teams", "session-"+short, "team.json"))

	files, err := collectSessionFiles(main, home)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[filepath.ToSlash(f.RelPath)] = true
	}
	want := []string{
		"root/projects/-proj/" + uuid + ".jsonl",
		"root/projects/-proj/" + uuid + "/subagents/agent-a.jsonl",
		"root/projects/-proj/" + uuid + "/subagents/agent-a.meta.json",
		"root/tasks/" + uuid + "/1.json",
		"root/teams/" + uuid + "/config.json",
		"root/tasks/session-" + short + "/2.json",
		"root/teams/session-" + short + "/team.json",
	}
	sort.Strings(want)
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing %s (got %v)", w, got)
		}
	}
	// The entry (main transcript) must be present with its expected RelPath.
	entry := "root/projects/-proj/" + uuid + ".jsonl"
	if !got[entry] {
		t.Errorf("entry %s not collected", entry)
	}
}

func TestCollectSessionFilesSkipsMissingOptional(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, "projects", "-proj")
	uuid := "abc12345-0000-0000-0000-000000000000"
	main := filepath.Join(projects, uuid+".jsonl")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := collectSessionFiles(main, home)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want only main transcript, got %d: %v", len(files), files)
	}
}

func TestCollectSessionFilesEmptyHome(t *testing.T) {
	_, err := collectSessionFiles("/some/path.jsonl", "")
	if err == nil {
		t.Fatal("expected error for empty home, got nil")
	}
}

// A path readable under ~/.claude but outside projects/ (e.g. a credentials file)
// must be rejected: export is for sessions, not arbitrary home files.
func TestCollectSessionFilesRejectsNonSessionPath(t *testing.T) {
	home := t.TempDir()
	secret := filepath.Join(home, ".credentials.json")
	if err := os.WriteFile(secret, []byte(`{"token":"s3cret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := collectSessionFiles(secret, home); err == nil {
		t.Fatal("expected rejection of a non-session path, got nil")
	}

	// A .jsonl directly under home (not under projects/) is also not a session.
	stray := filepath.Join(home, "stray.jsonl")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := collectSessionFiles(stray, home); err == nil {
		t.Fatal("expected rejection of a .jsonl outside projects/, got nil")
	}
}

// A symlink planted in the session tree must not be followed to its target's
// bytes, which could live outside home.
func TestCollectSessionFilesSkipsSymlinks(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, "projects", "-proj")
	uuid := "sym12345-0000-0000-0000-000000000000"
	if err := os.MkdirAll(filepath.Join(projects, uuid, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(projects, uuid+".jsonl")
	if err := os.WriteFile(main, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A subagent-dir entry symlinked to a file outside home.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(projects, uuid, "subagents", "evil.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	files, err := collectSessionFiles(main, home)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, f := range files {
		if strings.Contains(f.RelPath, "evil.jsonl") {
			t.Fatalf("symlink was collected: %+v", files)
		}
	}
}

// The entry itself being a symlink is rejected outright (fail closed).
func TestCollectSessionFilesRejectsSymlinkEntry(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, "projects", "-proj")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(projects, "link.jsonl")
	if err := os.Symlink(outside, entry); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := collectSessionFiles(entry, home); err == nil {
		t.Fatal("expected rejection of a symlinked entry, got nil")
	}
}

// TestCollectSessionFilesTeamMember covers the team-member path with real
// fixtures, so DiscoverTeamSessions and the parser run end-to-end (no seams).
func TestCollectSessionFilesTeamMember(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, "projects", "-proj")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}

	// Parent transcript: valid JSONL with a Task tool spawning a team member.
	parentUUID := "cc000001-0000-0000-0000-000000000000"
	main := filepath.Join(projects, parentUUID+".jsonl")
	if err := os.WriteFile(main, []byte(parentJSONL), 0o644); err != nil {
		t.Fatal(err)
	}

	// Team member file: sibling .jsonl in the same project dir.
	// First line carries teamName+agentName so ReadTeamSessionMeta recognises it.
	memberFile := filepath.Join(projects, "worker-session.jsonl")
	if err := os.WriteFile(memberFile, []byte(memberJSONL), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := collectSessionFiles(main, home)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	got := map[string]bool{}
	for _, f := range files {
		got[filepath.ToSlash(f.RelPath)] = true
	}

	wantMember := "root/projects/-proj/worker-session.jsonl"
	if !got[wantMember] {
		t.Errorf("team member file not collected; want %s, got %v", wantMember, got)
	}
	wantParent := "root/projects/-proj/" + parentUUID + ".jsonl"
	if !got[wantParent] {
		t.Errorf("parent transcript not collected; want %s, got %v", wantParent, got)
	}
}
