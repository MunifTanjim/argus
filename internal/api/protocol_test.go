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

func TestTrustLogSyncWireShape(t *testing.T) {
	if MethodTrustLogSync != "trustlog.sync" {
		t.Fatalf("method = %q", MethodTrustLogSync)
	}

	var res TrustLogSyncResult
	if err := json.Unmarshal([]byte(`{"entries":["AQ=="],"want":["Ag=="]}`), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0][0] != 1 {
		t.Fatalf("entries decoded wrong: %v", res.Entries)
	}
	if len(res.Want) != 1 || res.Want[0][0] != 2 {
		t.Fatalf("want decoded wrong: %v", res.Want)
	}
}

func TestTrustLogSyncOfferWireShape(t *testing.T) {
	b, err := json.Marshal(TrustLogSyncParams{Known: [][]byte{{1, 2, 3}}, Truncated: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(b), `{"known":["AQID"],"truncated":true}`; got != want {
		t.Fatalf("params wire shape = %s, want %s", got, want)
	}

	var res TrustLogSyncResult
	if err := json.Unmarshal([]byte(`{"entries":["AQ=="],"disjoint":true}`), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Disjoint || len(res.Entries) != 1 {
		t.Fatalf("decoded wrong: %+v", res)
	}
}

func TestTrustLogPushAndChangedWireShape(t *testing.T) {
	if MethodTrustLogPush != "trustlog.push" {
		t.Fatalf("method = %q", MethodTrustLogPush)
	}

	b, err := json.Marshal(TrustLogPushParams{Entries: [][]byte{{9}}})
	if err != nil {
		t.Fatalf("marshal push: %v", err)
	}
	if got, want := string(b), `{"entries":["CQ=="]}`; got != want {
		t.Fatalf("push wire shape = %s, want %s", got, want)
	}

	var ch TrustLogChangedParams
	if err := json.Unmarshal([]byte(`{"heads":["AQ=="]}`), &ch); err != nil {
		t.Fatalf("unmarshal changed: %v", err)
	}
	if len(ch.Heads) != 1 || ch.Heads[0][0] != 1 {
		t.Fatalf("heads decoded wrong: %v", ch.Heads)
	}
}
