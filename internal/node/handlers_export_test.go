package node

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bundle"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// newTestNode builds a Node with real adapters and a test version stamp.
// It uses an empty tmux-client map so no tmux binary is required.
func newTestNode(t *testing.T) *Node {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetVersion("test")
	return d
}

func TestHandleExportBundle(t *testing.T) {
	home := t.TempDir()
	// claudeHome() returns filepath.Join(os.UserHomeDir(), ".claude"), so put the
	// transcript under {HOME}/.claude/projects/-proj/... so it is collected.
	claudeHome := filepath.Join(home, ".claude")
	projects := filepath.Join(claudeHome, "projects", "-proj")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(projects, "abc12345-0000-0000-0000-000000000000.jsonl")
	if err := os.WriteFile(main, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home) // claudeHome() resolves under here

	d := newTestNode(t)
	params, _ := json.Marshal(api.ExportBundleParams{
		Agent: "claude", TranscriptPath: main,
		Metadata: bundle.Metadata{Title: "demo"},
	})
	res, err := d.handleExportBundle(context.Background(), params)
	if err != nil {
		t.Fatalf("handleExportBundle: %v", err)
	}
	out := res.(api.ExportBundleResult)
	if out.Filename == "" || len(out.Data) == 0 {
		t.Fatalf("empty result: %+v", out)
	}
	m, err := bundle.Read(bytes.NewReader(out.Data), t.TempDir())
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	if m.Agent != "claude" || m.Metadata.Title != "demo" || m.FormatVersion != bundle.FormatVersion {
		t.Fatalf("manifest wrong: %+v", m)
	}
	if m.ArgusVersion == "" || m.ExportedAt == "" {
		t.Fatalf("node did not stamp version/time: %+v", m)
	}
}
