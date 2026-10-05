package codex

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/codextool"
	"github.com/MunifTanjim/argus/internal/session"
)

func fileChangeItemJSON() map[string]any {
	return map[string]any{
		"type": "fileChange", "id": "i1", "status": "inProgress",
		"changes": []map[string]any{
			{"path": "a.go", "kind": map[string]any{"type": "update", "move_path": nil}, "diff": "@@ -1 +1 @@\n-old\n+new\n"},
			{"path": "b.go", "kind": map[string]any{"type": "add"}, "diff": "package b\n"},
		},
	}
}

func TestPatchInputRendersAsApplyPatch(t *testing.T) {
	mv := "c2.go"
	changes := []fileUpdateChange{
		{Path: "a.go", Diff: "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"},
		{Path: "b.go", Diff: "package b\n"},
		{Path: "c.go", Diff: "@@ -1 +1 @@\n-x\n+y\n\n\nMoved to: c2.go"},
		{Path: "d.go"},
	}
	changes[0].Kind.Type = "update"
	changes[1].Kind.Type = "add"
	changes[2].Kind.Type, changes[2].Kind.MovePath = "update", &mv
	changes[3].Kind.Type = "delete"
	files, ok := codextool.ParsePatch(patchInput(changes))
	if !ok || len(files) != 4 {
		t.Fatalf("parse = %+v, %v", files, ok)
	}
	if f := files[0]; f.Op != codextool.PatchUpdate || len(f.Lines) != 3 || f.Lines[1] != (codextool.PatchLine{Kind: '-', Text: "old"}) {
		t.Errorf("update = %+v", f)
	}
	if f := files[1]; f.Op != codextool.PatchAdd || len(f.Lines) != 1 || f.Lines[0] != (codextool.PatchLine{Kind: '+', Text: "package b"}) {
		t.Errorf("add = %+v", f)
	}
	if f := files[2]; f.MoveTo != "c2.go" || len(f.Lines) != 3 {
		t.Errorf("move = %+v", f)
	}
	if f := files[3]; f.Op != codextool.PatchDelete {
		t.Errorf("delete = %+v", f)
	}
}

func fileChangeApproval() map[string]any {
	return map[string]any{"threadId": "t1", "turnId": "u1", "itemId": "i1", "startedAtMs": 0, "reason": nil, "grantRoot": "/w"}
}

func TestFileChangeApprovalShowsStartedPatch(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	_, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.push("item/started", map[string]any{"threadId": "t1", "turnId": "u1", "item": fileChangeItemJSON()})
	f.request(1, "item/fileChange/requestApproval", fileChangeApproval())
	s := waitSession(t, reg, "t1", func(s session.Session) bool { return s.Interaction != nil && s.Interaction.ToolName == "apply_patch" })
	if files, ok := codextool.ParsePatch(s.Interaction.ToolInput); !ok || len(files) != 2 {
		t.Fatalf("tool input = %q", s.Interaction.ToolInput)
	}
	if s.Interaction.Message == "" {
		t.Error("grantRoot must be shown")
	}
}

// A request replayed on subscribe has no item/started; its patch comes from the
// turn's history.
func TestReplayedFileChangeApprovalReadsHistory(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")},
		items: map[string][]any{"t1": {map[string]any{"item": fileChangeItemJSON()}}}}
	_, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.request(1, "item/fileChange/requestApproval", fileChangeApproval())
	s := waitSession(t, reg, "t1", func(s session.Session) bool { return s.Interaction != nil && s.Interaction.ToolName == "apply_patch" })
	if files, ok := codextool.ParsePatch(s.Interaction.ToolInput); !ok || len(files) != 2 {
		t.Fatalf("tool input = %q", s.Interaction.ToolInput)
	}
}
