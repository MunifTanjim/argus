package api

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestPlainChannelRoundTrip(t *testing.T) {
	c := NewPlainChannel("c1")
	params := json.RawMessage(`{"session_id":"s1"}`)
	id := json.RawMessage(`1`)
	frame, err := c.SealRequestFrame(EncodingJSON, &id, "sessions.tasks", "node-a", params)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	f, err := ParseRelayFrame(EncodingJSON, frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := c.OpenParams(f)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != string(params) {
		t.Fatalf("round-trip mismatch: got %s want %s", got, params)
	}
}

func testChannelPairs(t *testing.T) map[string][2]*Channel {
	t.Helper()
	clientSess, nodeSess := newE2ESessionPair(t, "node-1", "chan-9")
	return map[string][2]*Channel{
		"noise": {NewChannel("chan-9", nodeSess), NewChannel("chan-9", clientSess)},
		"plain": {NewPlainChannel("chan-9"), NewPlainChannel("chan-9")},
	}
}

func TestChannelCBORRoundTrip(t *testing.T) {
	for name, pair := range testChannelPairs(t) {
		t.Run(name, func(t *testing.T) {
			sender, receiver := pair[0], pair[1]
			params, err := CBORMarshal(TerminalOutput{TermID: "t1", Data: bytes.Repeat([]byte{0xab}, 70000)})
			if err != nil {
				t.Fatal(err)
			}
			frame, err := sender.SealNotificationFrame(EncodingCBOR, MethodTerminalOutput, RouteHeader{}, params)
			if err != nil {
				t.Fatal(err)
			}
			f, err := ParseRelayFrame(EncodingCBOR, frame)
			if err != nil {
				t.Fatal(err)
			}
			if f.Method != MethodTerminalOutput || f.Route.ChanID != "chan-9" {
				t.Fatalf("frame = %q %+v", f.Method, f.Route)
			}
			got, err := receiver.OpenParams(f)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, params) {
				t.Fatal("params mismatch")
			}

			var outer map[string]any
			if err := CBORUnmarshal(frame, &outer); err != nil {
				t.Fatal(err)
			}
			var keys []string
			for k := range outer {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if want := []string{"body", "jsonrpc", "method", "route"}; !reflect.DeepEqual(keys, want) {
				t.Fatalf("outer keys = %v, want %v", keys, want)
			}
		})
	}
}

func TestChannelCBORResponse(t *testing.T) {
	for name, pair := range testChannelPairs(t) {
		t.Run(name, func(t *testing.T) {
			node, client := pair[0], pair[1]
			id := json.RawMessage(`7`)

			frame, err := node.SealResponseFrame(EncodingCBOR, &id, nil, &RPCError{Code: -32600, Message: "unknown term_id: x"})
			if err != nil {
				t.Fatal(err)
			}
			f, err := ParseRelayFrame(EncodingCBOR, frame)
			if err != nil {
				t.Fatal(err)
			}
			if f.ID == nil || string(*f.ID) != "7" {
				t.Fatalf("id = %v", f.ID)
			}
			res, rpcErr, err := client.OpenResponse(f)
			if err != nil {
				t.Fatal(err)
			}
			if res != nil || rpcErr == nil || rpcErr.Code != -32600 || rpcErr.Message != "unknown term_id: x" {
				t.Fatalf("res=%v err=%+v", res, rpcErr)
			}

			result, _ := CBORMarshal(map[string]string{"ok": "yes"})
			frame, err = node.SealResponseFrame(EncodingCBOR, &id, result, nil)
			if err != nil {
				t.Fatal(err)
			}
			f, _ = ParseRelayFrame(EncodingCBOR, frame)
			res, rpcErr, err = client.OpenResponse(f)
			if err != nil || rpcErr != nil || !bytes.Equal(res, result) {
				t.Fatalf("res=%x err=%+v %v", res, rpcErr, err)
			}

			frame, err = node.SealResponseFrame(EncodingCBOR, &id, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			f, _ = ParseRelayFrame(EncodingCBOR, frame)
			res, rpcErr, err = client.OpenResponse(f)
			if err != nil || rpcErr != nil || len(res) != 0 {
				t.Fatalf("nil result: res=%x err=%+v %v", res, rpcErr, err)
			}
		})
	}
}

func TestChannelCBORFrameSize(t *testing.T) {
	const size = 32 * 1024
	chunk := bytes.Repeat([]byte{0xff}, size)
	ch := testChannelPairs(t)["noise"][0]

	cborParams, _ := CBORMarshal(TerminalOutput{TermID: "t1", Data: chunk})
	cborFrame, err := ch.SealNotificationFrame(EncodingCBOR, MethodTerminalOutput, RouteHeader{TermID: "t1"}, cborParams)
	if err != nil {
		t.Fatal(err)
	}
	if len(cborFrame) > size+128 {
		t.Fatalf("cbor frame = %d bytes, want <= %d", len(cborFrame), size+128)
	}

	jsonParams, _ := json.Marshal(TerminalOutput{TermID: "t1", Data: chunk})
	jsonFrame, err := ch.SealNotificationFrame(EncodingJSON, MethodTerminalOutput, RouteHeader{TermID: "t1"}, jsonParams)
	if err != nil {
		t.Fatal(err)
	}
	if float64(len(jsonFrame)) < 1.7*size {
		t.Fatalf("json frame = %d bytes, want >= %v", len(jsonFrame), 1.7*size)
	}
}

func TestChannelMixedEncodingsShareNonceOrder(t *testing.T) {
	pair := testChannelPairs(t)["noise"]
	sender, receiver := pair[0], pair[1]
	encs := []Encoding{EncodingJSON, EncodingCBOR, EncodingJSON}
	for i, enc := range encs {
		params, _ := enc.Marshal(map[string]int{"n": i})
		frame, err := sender.SealNotificationFrame(enc, "test.note", RouteHeader{}, params)
		if err != nil {
			t.Fatal(err)
		}
		f, err := ParseRelayFrame(enc, frame)
		if err != nil {
			t.Fatal(err)
		}
		got, err := receiver.OpenParams(f)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if !bytes.Equal(got, params) {
			t.Fatalf("frame %d mismatch", i)
		}
	}
}
