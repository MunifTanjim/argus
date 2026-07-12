package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestScanRolloutDropsUnterminatedFinalLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")

	terminated := `{"timestamp":"t1","type":"turn_context","payload":{"model":"gpt"}}` + "\n"
	unterminated := `{"timestamp":"t2","type":"response_item","payload":{"type":"message"`
	if err := os.WriteFile(path, []byte(terminated+unterminated), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, err := scanRollout(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (unterminated final line dropped)", len(lines))
	}
}

func TestScanRolloutFromDefersPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")

	full := `{"timestamp":"t1","type":"turn_context","payload":{"model":"gpt"}}
{"timestamp":"t2","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}
`
	partial := `{"timestamp":"t3","type":"response`
	if err := os.WriteFile(path, []byte(full+partial), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, off, err := scanRolloutFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (partial deferred)", len(lines))
	}
	if off != int64(len(full)) {
		t.Fatalf("offset = %d, want %d (last newline)", off, len(full))
	}

	rest := `_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"yo"}]}}` + "\n"
	appendFile(t, path, rest)

	lines2, off2, err := scanRolloutFrom(path, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines2) != 1 {
		t.Fatalf("got %d lines, want 1 (completed line)", len(lines2))
	}
	if want := int64(len(full) + len(partial) + len(rest)); off2 != want {
		t.Fatalf("offset = %d, want %d (end of file)", off2, want)
	}
}

func TestStreamingRefreshIncrementalEqualsWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")

	seg1 := `{"timestamp":"t1","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}
`
	seg2 := `{"timestamp":"t2","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}
`
	if err := os.WriteFile(path, []byte(seg1), 0o644); err != nil {
		t.Fatal(err)
	}

	st := NewStreamingTranscript(path, "", false)
	if _, err := st.Refresh(); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	appendFile(t, path, seg2)
	got, err := st.Refresh()
	if err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	want, err := ReadTranscriptView(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want.Chunks) {
		t.Fatalf("incremental chunks != whole-file parse\n got: %+v\nwant: %+v", got, want.Chunks)
	}
}

func TestStreamingRefreshTruncationReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")

	big := `{"timestamp":"t1","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first session line one"}]}}
{"timestamp":"t2","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first session reply"}]}}
`
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStreamingTranscript(path, "", false)
	if _, err := st.Refresh(); err != nil {
		t.Fatal(err)
	}

	small := `{"timestamp":"t3","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"new"}]}}
`
	if err := os.WriteFile(path, []byte(small), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	want, err := ReadTranscriptView(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want.Chunks) {
		t.Fatalf("after truncation, chunks != whole-file parse\n got: %+v\nwant: %+v", got, want.Chunks)
	}
}

func TestReadTranscriptViewStampsSubagent(t *testing.T) {
	view, err := ReadTranscriptView("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("ReadTranscriptView: %v", err)
	}
	var sub *transcript.Item
	for i := range view.Chunks {
		for j := range view.Chunks[i].Items {
			if view.Chunks[i].Items[j].ToolName == "spawn_agent" {
				sub = &view.Chunks[i].Items[j]
			}
		}
	}
	if sub == nil {
		t.Fatal("no spawn_agent item")
	}
	if len(sub.Subagents) == 0 || sub.Subagents[0].ID == "" {
		t.Fatal("subagent id not linked")
	}
}

func TestStreamingRefreshReturnsFullList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")

	lines := `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}
{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	st := NewStreamingTranscript(path, "", false)

	chunks1, err := st.Refresh()
	if err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if len(chunks1) == 0 {
		t.Fatal("first Refresh returned no chunks")
	}

	// Second call with no file change must return the full list, not a delta.
	chunks2, err := st.Refresh()
	if err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if len(chunks2) != len(chunks1) {
		t.Fatalf("second Refresh returned %d chunks, want %d (full list)", len(chunks2), len(chunks1))
	}
}

func TestFindToolDetail(t *testing.T) {
	view, err := ReadTranscriptView("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var toolID string
	for _, c := range view.Chunks {
		for _, it := range c.Items {
			if it.ToolName == "update_plan" {
				toolID = it.ToolID
			}
		}
	}
	if toolID == "" {
		t.Fatal("no update_plan tool id")
	}
	det, ok, err := FindToolDetail("testdata/rollout-parent.jsonl", "", toolID)
	if err != nil || !ok {
		t.Fatalf("FindToolDetail ok=%v err=%v", ok, err)
	}
	if det.Result != "Plan updated" {
		t.Fatalf("result = %q, want 'Plan updated'", det.Result)
	}
}

func TestFindToolDetailSkill(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	text := "<skill>\n<name>test:skill</name>\n<path>/test/path/SKILL.md</path>\n# Skill Body\nSome skill content.\n</skill>"
	line, err := json.Marshal(map[string]any{
		"type": "response_item",
		"payload": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": text},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := ReadTranscriptView(p)
	if err != nil {
		t.Fatalf("ReadTranscriptView: %v", err)
	}
	var toolID string
	for _, c := range view.Chunks {
		for _, it := range c.Items {
			if it.Kind == transcript.ItemSkill {
				toolID = it.ToolID
			}
		}
	}
	if toolID == "" {
		t.Fatal("no skill item toolID found")
	}
	det, ok, err := FindToolDetail(p, "", toolID)
	if err != nil || !ok {
		t.Fatalf("FindToolDetail ok=%v err=%v", ok, err)
	}
	if det.Result != "# Skill Body\nSome skill content." {
		t.Fatalf("result = %q, want skill body", det.Result)
	}
}
