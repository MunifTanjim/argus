package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSnapshotName(t *testing.T) {
	s, ok := parseSnapshotName("019f267e-2514-7ad0-a3eb-f748366490b0.1783068127073097000.sh")
	if !ok {
		t.Fatal("should parse a valid snapshot name")
	}
	if s.threadID != "019f267e-2514-7ad0-a3eb-f748366490b0" {
		t.Errorf("threadID = %q", s.threadID)
	}
	if s.startNS != 1783068127073097000 {
		t.Errorf("startNS = %d", s.startNS)
	}
	if _, ok := parseSnapshotName(".sh"); ok {
		t.Error("empty thread id should not parse")
	}
	if _, ok := parseSnapshotName("nodot.sh"); ok {
		t.Error("name without a timestamp separator should not parse")
	}
}

func TestReadSnapshotTmux(t *testing.T) {
	dir := t.TempDir()
	tmuxed := filepath.Join(dir, "a.sh")
	body := "#!/bin/sh\nsome_func() { echo hi; }\nexport TMUX=/private/tmp/tmux-501/argus,1088,0\nexport TMUX_PANE=%73\nexport TMUX_PLUGIN_MANAGER_PATH=/x\n"
	if err := os.WriteFile(tmuxed, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	pane, socket := readSnapshotTmux(tmuxed)
	if pane != "%73" {
		t.Errorf("pane = %q; want %%73", pane)
	}
	if socket != "/private/tmp/tmux-501/argus" { // ",pid,grp" suffix stripped
		t.Errorf("socket = %q; want the socket path only", socket)
	}

	bare := filepath.Join(dir, "b.sh")
	if err := os.WriteFile(bare, []byte("export FOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pane, socket := readSnapshotTmux(bare); pane != "" || socket != "" {
		t.Errorf("non-tmux snapshot should yield empty pane/socket, got %q %q", pane, socket)
	}
}

func TestListSnapshotsDedupAndPaneless(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	dir := filepath.Join(home, "shell_snapshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("t1.100.sh", "export TMUX=/s/default,1,0\nexport TMUX_PANE=%1\n")
	write("t1.200.sh", "export TMUX=/s/default,1,0\nexport TMUX_PANE=%2\n") // newer → wins
	write("t2.50.sh", "export FOO=bar\n")                                   // paneless
	write("notes.txt", "ignore me")

	snaps, err := listSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots; want 2: %+v", len(snaps), snaps)
	}
	by := map[string]snapshot{}
	for _, s := range snaps {
		by[s.threadID] = s
	}
	if by["t1"].paneID != "%2" {
		t.Errorf("newest snapshot should win: pane = %q, want %%2", by["t1"].paneID)
	}
	if by["t2"].paneID != "" {
		t.Errorf("paneless snapshot should have empty pane, got %q", by["t2"].paneID)
	}
}

func TestListSnapshotsMissingDir(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir()) // no shell_snapshots subdir
	snaps, err := listSnapshots()
	if err != nil || len(snaps) != 0 {
		t.Fatalf("missing dir should yield no snapshots and no error: %v %v", snaps, err)
	}
}
