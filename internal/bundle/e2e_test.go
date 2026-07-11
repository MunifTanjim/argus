package bundle_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/bundle"
)

func TestExportViewFidelity(t *testing.T) {
	// multi_turn.jsonl has no subagents, so path-derived lookups (subagent dir,
	// team members) return nothing on both sides and DeepEqual holds without any
	// path-sensitive fields.
	fixtureSource := filepath.Join("..", "adapter", "claudecode", "parser", "testdata", "multi_turn.jsonl")
	if _, err := os.Stat(fixtureSource); err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}

	// Fake $HOME so claudeHome() and CollectSessionFiles resolve under our temp dir.
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Place the fixture under <home>/.claude/projects/-proj/<uuid>.jsonl.
	projDir := filepath.Join(home, ".claude", "projects", "-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	uuid := "abc12345-0000-0000-0000-000000000000"
	mainPath := filepath.Join(projDir, uuid+".jsonl")
	data, err := os.ReadFile(fixtureSource)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(mainPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	a := adapters.Default()

	orig, err := a.ReadTranscriptView(mainPath)
	if err != nil {
		t.Fatalf("ReadTranscriptView (orig): %v", err)
	}
	if len(orig.Chunks) == 0 {
		t.Fatal("fixture produced zero chunks; pick a richer fixture")
	}

	files, err := a.CollectSessionFiles(mainPath)
	if err != nil {
		t.Fatalf("CollectSessionFiles: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("CollectSessionFiles returned no files; HOME env setup failed")
	}

	srcs := make([]bundle.SourceFile, 0, len(files))
	entry := ""
	for _, f := range files {
		srcs = append(srcs, bundle.SourceFile{ArchivePath: f.RelPath, SourcePath: f.AbsPath})
		if f.AbsPath == mainPath {
			entry = f.RelPath
		}
	}
	if entry == "" {
		t.Fatalf("main transcript not in collected files; files=%v", files)
	}

	var buf bytes.Buffer
	if err := bundle.Write(&buf, bundle.Manifest{
		FormatVersion: bundle.FormatVersion,
		Agent:         "claude",
		Entry:         entry,
	}, srcs); err != nil {
		t.Fatalf("bundle.Write: %v", err)
	}

	dest := t.TempDir()
	m, err := bundle.Read(&buf, dest)
	if err != nil {
		t.Fatalf("bundle.Read: %v", err)
	}
	extractedPath := filepath.Join(dest, filepath.FromSlash(m.Entry))
	got, err := a.ReadTranscriptView(extractedPath)
	if err != nil {
		t.Fatalf("ReadTranscriptView (extracted): %v", err)
	}

	if !reflect.DeepEqual(orig.Chunks, got.Chunks) {
		t.Fatalf("fidelity mismatch: orig=%d chunks, got=%d chunks", len(orig.Chunks), len(got.Chunks))
	}
}
