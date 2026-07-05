package antigravity

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestConversationIDFromArgv(t *testing.T) {
	got := conversationIDFromArgv("agy --conversation=247419d6-eef1-49cb-855c-609e7e13849b")
	if got != "247419d6-eef1-49cb-855c-609e7e13849b" {
		t.Fatalf("got %q", got)
	}
	if conversationIDFromArgv("agy -c") != "" {
		t.Fatal("no --conversation should yield empty")
	}
}

func TestBuildDiscoveredBindsPaneByTTY(t *testing.T) {
	procs := []agyProc{{tty: "ttys003", conversationID: "conv-a", cwd: "/home/u/proj", transcriptPath: "/brain/conv-a/transcript_full.jsonl"}}
	panes := map[string]paneInfo{
		"ttys003": {server: session.TmuxServerDefault, paneID: "%5", sessionName: "main", windowIndex: 2, currentPath: "/home/u/proj"},
	}
	out := buildDiscovered(procs, panes)
	if len(out) != 1 {
		t.Fatalf("want 1 session, got %d", len(out))
	}
	d := out[0]
	if d.AgentSessionID != "conv-a" || !d.HasPane || d.PaneID != "%5" || d.Frontend != session.FrontendTmux {
		t.Fatalf("pane binding wrong: %+v", d)
	}
	if d.TranscriptPath != "/brain/conv-a/transcript_full.jsonl" {
		t.Fatalf("transcript path not propagated: %q", d.TranscriptPath)
	}
	if d.Server != session.TmuxServerDefault {
		t.Fatalf("server = %q, want %q", d.Server, session.TmuxServerDefault)
	}
	if d.Cwd != "/home/u/proj" {
		t.Fatalf("cwd = %q", d.Cwd)
	}
}

func TestBuildDiscoveredPanelessIsExternal(t *testing.T) {
	procs := []agyProc{{tty: "ttys009", conversationID: "conv-b", cwd: "/x"}}
	out := buildDiscovered(procs, map[string]paneInfo{})
	if len(out) != 1 || out[0].HasPane || out[0].Frontend != session.FrontendExternal {
		t.Fatalf("paneless should be external: %+v", out)
	}
}
