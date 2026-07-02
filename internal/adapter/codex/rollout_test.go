package codex

import (
	"os"
	"testing"
)

func TestScanRollout(t *testing.T) {
	lines, err := scanRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("scanRollout: %v", err)
	}
	if len(lines) != 83 {
		t.Fatalf("want 83 lines, got %d", len(lines))
	}
	var meta, resp, evt, tctx int
	for _, l := range lines {
		switch l.Type {
		case "session_meta":
			meta++
		case "response_item":
			resp++
		case "event_msg":
			evt++
		case "turn_context":
			tctx++
		}
	}
	if meta != 1 || tctx != 4 {
		t.Fatalf("want meta=1 tctx=4, got meta=%d tctx=%d", meta, tctx)
	}
	if resp == 0 || evt == 0 {
		t.Fatalf("want resp>0 evt>0, got resp=%d evt=%d", resp, evt)
	}
}

func TestScanRolloutMissingFile(t *testing.T) {
	lines, err := scanRollout("testdata/does-not-exist.jsonl")
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if lines != nil {
		t.Fatalf("want nil, got %d lines", len(lines))
	}
}

func TestScanRolloutSkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/x.jsonl"
	content := "{\"type\":\"session_meta\",\"payload\":{}}\n" +
		"not json\n" +
		"\n" +
		"{\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\"}}\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := scanRollout(p)
	if err != nil {
		t.Fatalf("scanRollout: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 valid lines, got %d", len(lines))
	}
}
