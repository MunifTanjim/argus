package antigravity

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func writeConversation(t *testing.T, home, id, cwd string, mod time.Time) {
	t.Helper()
	convDir := filepath.Join(home, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(convDir, id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE trajectory_metadata_blob(id text DEFAULT "main", data blob)`); err != nil {
		t.Fatal(err)
	}
	blob := []byte("\x0a\x2ffile://" + cwd) // arbitrary framing + file:// URI
	if _, err := db.Exec(`INSERT INTO trajectory_metadata_blob(id, data) VALUES('main', ?)`, blob); err != nil {
		t.Fatal(err)
	}
	db.Close()

	tdir := filepath.Join(home, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpath := filepath.Join(tdir, "transcript_full.jsonl")
	if err := os.WriteFile(tpath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(tpath, mod, mod)
}

func TestAntigravityHistoryProjectsAndSessions(t *testing.T) {
	home := t.TempDir()
	homeDirOverride = home
	t.Cleanup(func() { homeDirOverride = "" })
	now := time.Now()
	writeConversation(t, home, "11111111-1111-1111-1111-111111111111", "/work/proj", now.Add(-2*time.Hour))
	writeConversation(t, home, "22222222-2222-2222-2222-222222222222", "/work/proj", now.Add(-1*time.Hour))

	projects, err := ListHistoryProjects()
	if err != nil {
		t.Fatal(err)
	}
	var found *int
	for i := range projects {
		if projects[i].ProjectDir == "/work/proj" {
			found = &projects[i].SessionCount
		}
	}
	if found == nil || *found != 2 {
		t.Fatalf("expected /work/proj with 2 sessions, got %+v", projects)
	}

	page, err := ListHistorySessions("/work/proj", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("sessions = %d, want 2", len(page.Items))
	}
	if page.Items[0].TranscriptPath == "" {
		t.Errorf("session missing transcript path: %+v", page.Items[0])
	}
}

func TestConversationWorkspaceParsesFileURI(t *testing.T) {
	home := t.TempDir()
	homeDirOverride = home
	t.Cleanup(func() { homeDirOverride = "" })
	writeConversation(t, home, "33333333-3333-3333-3333-333333333333", "/some/where", time.Now())
	if got := conversationWorkspace("33333333-3333-3333-3333-333333333333"); got != "/some/where" {
		t.Errorf("conversationWorkspace = %q, want /some/where", got)
	}
}
