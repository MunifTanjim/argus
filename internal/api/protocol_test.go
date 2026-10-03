package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestTranscriptDeltaJSONTags(t *testing.T) {
	d := TranscriptDelta{SubID: "s1", FromIndex: 2, Chunks: []transcript.Chunk{{ID: "2"}}}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	want := `{"sub_id":"s1","from_index":2,"chunks":[{"id":"2","kind":""}]}`
	if got != want {
		t.Fatalf("delta json = %s, want %s", got, want)
	}
}

func TestTranscriptSubscribeParamsRoundTrip(t *testing.T) {
	in := TranscriptSubscribeParams{SubID: "s1", SessionID: "d:1", AgentID: "a1", HaveChunks: 3}
	b, _ := json.Marshal(in)
	var out TranscriptSubscribeParams
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}

func TestTerminalOpenParamsDecode(t *testing.T) {
	raw := json.RawMessage(`{"term_id":"t1","session_id":"n1-%3","cols":80,"rows":24}`)
	p, err := Decode[TerminalOpenParams](raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.TermID != "t1" || p.SessionID != "n1-%3" || p.Cols != 80 || p.Rows != 24 {
		t.Fatalf("bad decode: %+v", p)
	}
}

func TestTerminalWireShape(t *testing.T) {
	b, err := json.Marshal(Terminal{ID: "@1", Name: "build", Cwd: "~/src", Command: "zsh", Attached: true, NodeID: "n1", NodeLabel: "home"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"@1","name":"build","cwd":"~/src","command":"zsh","attached":true,"node_id":"n1","node_label":"home"}`
	if string(b) != want {
		t.Errorf("Terminal = %s\nwant %s", b, want)
	}
	b, _ = json.Marshal(NodeCapabilities{SpawnSession: true, Terminal: true})
	if string(b) != `{"spawn_session":true,"terminal":true}` {
		t.Errorf("NodeCapabilities = %s", b)
	}
	b, _ = json.Marshal(TerminalOpenParams{TermID: "t1", TerminalID: "@1", Cols: 80, Rows: 24})
	if !strings.Contains(string(b), `"terminal_id":"@1"`) {
		t.Errorf("TerminalOpenParams = %s, want terminal_id", b)
	}
	b, _ = json.Marshal(TerminalRenameParams{TerminalID: "@1", Name: "x"})
	if string(b) != `{"terminal_id":"@1","name":"x"}` {
		t.Errorf("TerminalRenameParams = %s", b)
	}
}
