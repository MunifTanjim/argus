package api

import (
	"encoding/json"
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

func TestPushDeliverParamsRoundTrip(t *testing.T) {
	in := PushDeliverParams{Endpoint: "https://p/ep", Ciphertext: "AAAA", TTL: "1800", Urgency: "high"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out PushDeliverParams
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round-trip: %+v != %+v", out, in)
	}
	if MethodPushDeliver != "push.deliver" {
		t.Fatalf("method = %q", MethodPushDeliver)
	}
}
