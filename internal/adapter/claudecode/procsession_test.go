package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProcSession(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("37699.json", `{"pid":37699,"sessionId":"d10ac1db","cwd":"/repo/argus","name":"vendor-parser","status":"busy","version":"2.1.177"}`)
	write("1.json", `not json`)             // malformed
	write("2.json", `{"pid":2,"cwd":"/x"}`) // no sessionId

	ps, ok := readProcSession(dir, 37699)
	if !ok {
		t.Fatal("expected a hit for 37699.json")
	}
	if ps.SessionID != "d10ac1db" || ps.Cwd != "/repo/argus" || ps.Name != "vendor-parser" {
		t.Fatalf("parsed wrong: %+v", ps)
	}

	if _, ok := readProcSession(dir, 1); ok {
		t.Error("malformed json should return ok=false")
	}
	if _, ok := readProcSession(dir, 2); ok {
		t.Error("missing sessionId should return ok=false")
	}
	if _, ok := readProcSession(dir, 99999); ok {
		t.Error("missing file should return ok=false")
	}
	if _, ok := readProcSession(dir, 0); ok {
		t.Error("pid<=0 should return ok=false")
	}
}

func TestEffectiveSessionIDFollowsParkedJob(t *testing.T) {
	interactive := procSession{PID: 100, SessionID: "old-full-id", ParkedJobId: "newpre", Cwd: "/x"}
	bg := procSession{PID: 200, SessionID: "newpre-full-id", JobId: "newpre"}

	// Resolves the parked prefix to the bg job's full session id.
	if got := effectiveSessionID(interactive, []procSession{interactive, bg}, ""); got != "newpre-full-id" {
		t.Fatalf("parked -> bg jobId: got %q", got)
	}

	// No parked job: unchanged.
	plain := procSession{PID: 300, SessionID: "plain-id"}
	if got := effectiveSessionID(plain, []procSession{plain}, ""); got != "plain-id" {
		t.Fatalf("no parked job should be unchanged: got %q", got)
	}

	// No sibling bg proc: fall back to the transcript filename by prefix.
	projDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projDir, "newpre-full-id.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := effectiveSessionID(interactive, []procSession{interactive}, projDir); got != "newpre-full-id" {
		t.Fatalf("parked -> transcript glob: got %q", got)
	}

	// No sibling and no transcript: fall back to the raw sessionId.
	if got := effectiveSessionID(interactive, []procSession{interactive}, t.TempDir()); got != "old-full-id" {
		t.Fatalf("unresolvable parked job should fall back: got %q", got)
	}
}

func TestListProcSessions(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("100.json", `{"pid":100,"sessionId":"vs-1","entrypoint":"claude-vscode"}`)
	write("200.json", `{"pid":200,"sessionId":"cli-1","entrypoint":"cli"}`)
	write("bad.json", `not json`)              // skipped
	write("3.json", `{"pid":3,"cwd":"/x"}`)    // no sessionId → skipped
	write("notes.txt", `{"sessionId":"nope"}`) // non-json suffix → skipped
	if err := os.Mkdir(filepath.Join(dir, "sub.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := listProcSessions(dir)
	byID := map[string]procSession{}
	for _, ps := range got {
		byID[ps.SessionID] = ps
	}
	if len(byID) != 2 {
		t.Fatalf("want 2 valid proc-sessions, got %d (%v)", len(byID), got)
	}
	if byID["vs-1"].PID != 100 || byID["vs-1"].Entrypoint != "claude-vscode" {
		t.Errorf("vs-1 parsed wrong: %+v", byID["vs-1"])
	}
	if byID["cli-1"].PID != 200 {
		t.Errorf("cli-1 pid: want 200, got %d", byID["cli-1"].PID)
	}
	if listProcSessions("") != nil {
		t.Error("empty dir should return nil")
	}
}

func TestFindProcSessionByID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "111.json"),
		[]byte(`{"pid":111,"sessionId":"vs-1","entrypoint":"claude-vscode"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "222.json"),
		[]byte(`{"pid":222,"sessionId":"cli-1","entrypoint":"cli"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := findProcSessionByID(dir, "vs-1")
	if !ok || got.Entrypoint != "claude-vscode" || got.PID != 111 {
		t.Fatalf("vs-1: ok=%v entrypoint=%q pid=%d", ok, got.Entrypoint, got.PID)
	}
	got, ok = findProcSessionByID(dir, "cli-1")
	if !ok || got.Entrypoint != "cli" {
		t.Fatalf("cli-1: ok=%v entrypoint=%q", ok, got.Entrypoint)
	}
	if _, ok := findProcSessionByID(dir, "missing"); ok {
		t.Error("missing session id should not match")
	}
	if _, ok := findProcSessionByID("", "vs-1"); ok {
		t.Error("empty dir should not match")
	}
}
