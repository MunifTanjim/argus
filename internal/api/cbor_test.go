package api

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestCBORMessageKeys(t *testing.T) {
	id := uint64(7)
	b, err := CBORMarshal(cborMessage{JSONRPC: "2.0", ID: &id, Method: "terminal.input",
		Route: &RouteHeader{ChanID: "c12"}, Body: []byte{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := CBORUnmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"body", "id", "jsonrpc", "method", "route"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	route, ok := m["route"].(map[string]any)
	if !ok || len(route) != 1 || route["chan_id"] != "c12" {
		t.Fatalf("route = %#v", m["route"])
	}
	if body, _ := m["body"].([]byte); !bytes.Equal(body, []byte{1, 2}) {
		t.Fatalf("body = %#v", m["body"])
	}
}

func TestCBORDecodeLimits(t *testing.T) {
	var v any
	deep := append(bytes.Repeat([]byte{0x81}, 40), 0x00)
	if err := CBORUnmarshal(deep, &v); err == nil {
		t.Error("expected error for deeply nested input")
	}
	indef := []byte{0xbf, 0x61, 0x61, 0x01, 0xff}
	if err := CBORUnmarshal(indef, &v); err == nil {
		t.Error("expected error for indefinite-length map")
	}
	dup := []byte{0xa2, 0x61, 0x61, 0x01, 0x61, 0x61, 0x02}
	if err := CBORUnmarshal(dup, &v); err == nil {
		t.Error("expected error for duplicate map key")
	}
}

func TestCBORToJSONBytesAreBase64(t *testing.T) {
	b, err := CBORMarshal(map[string]any{"term_id": "t1", "data": []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	j, err := CBORToJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(j, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"term_id": "t1", "data": "aGk="}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
