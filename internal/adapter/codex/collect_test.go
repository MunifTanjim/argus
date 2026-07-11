package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/bundle"
)

const subagentChildID = "019f278e-50a5-7f83-91f2-c30e8ac18e19"

// seedParentWithChild lays out a parent rollout (from testdata) plus its spawned
// child rollout under home/sessions, returning the parent's path.
func seedParentWithChild(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "07", "03")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "rollout-2026-07-03T10-00-00-parent.jsonl")
	if err := os.WriteFile(parent, src, 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "rollout-2026-07-03T10-30-00-"+subagentChildID+".jsonl")
	line := `{"timestamp":"2026-07-03T10:30:00Z","type":"session_meta","payload":{"id":"` + subagentChildID + `","cwd":"/w"}}` + "\n"
	if err := os.WriteFile(child, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return parent
}

func TestCollectSessionFilesIncludesSubagents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	parent := seedParentWithChild(t, home)

	files, err := collectSessionFiles(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files (parent + child), got %d: %+v", len(files), files)
	}
	var haveChild bool
	for _, f := range files {
		if !strings.HasPrefix(f.RelPath, "root/sessions/2026/07/03/") {
			t.Fatalf("RelPath %q not rooted under root/sessions/", f.RelPath)
		}
		if strings.Contains(f.RelPath, subagentChildID) {
			haveChild = true
		}
	}
	if !haveChild {
		t.Fatalf("child rollout not collected: %+v", files)
	}
}

// TestSubagentFilePathRootRelative verifies resolution follows the passed rootPath
// (an extracted bundle), not the live ~/.codex.
func TestSubagentFilePathRootRelative(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir()) // live home has no matching child

	bundleHome := t.TempDir()
	parent := seedParentWithChild(t, bundleHome)

	p, ok := SubagentFilePath(parent, subagentChildID)
	if !ok {
		t.Fatal("SubagentFilePath should resolve child from the bundle root")
	}
	if !strings.HasPrefix(p, bundleHome) {
		t.Fatalf("resolved %q, want a path under bundle home %q", p, bundleHome)
	}

	// Empty root falls back to the live home, which lacks the child.
	if _, ok := SubagentFilePath("", subagentChildID); ok {
		t.Fatal("empty root must not resolve against the (childless) live home")
	}
}

// TestOfflineSubagentResolution: a subagent bundled from one home resolves from
// the extracted temp dir even when the live ~/.codex is empty.
func TestOfflineSubagentResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	parent := seedParentWithChild(t, home)

	files, err := collectSessionFiles(parent)
	if err != nil {
		t.Fatal(err)
	}
	srcs := make([]bundle.SourceFile, 0, len(files))
	var entry string
	for _, f := range files {
		srcs = append(srcs, bundle.SourceFile{ArchivePath: f.RelPath, SourcePath: f.AbsPath})
		if f.AbsPath == parent {
			entry = f.RelPath
		}
	}
	var buf bytes.Buffer
	if err := bundle.Write(&buf, bundle.Manifest{FormatVersion: bundle.FormatVersion, Agent: Agent, Entry: entry}, srcs); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	m, err := bundle.Read(&buf, dest)
	if err != nil {
		t.Fatal(err)
	}
	// Point the live home elsewhere: resolution must come from the bundle.
	t.Setenv("CODEX_HOME", t.TempDir())

	entryPath := filepath.Join(dest, filepath.FromSlash(m.Entry))
	if _, ok, err := ReadSubagentView(entryPath, subagentChildID); err != nil || !ok {
		t.Fatalf("offline subagent view: ok=%v err=%v", ok, err)
	}
}
