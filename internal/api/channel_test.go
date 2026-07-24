package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/e2e"
)

// channelPair returns two Channels sharing an established e2e session (client, node).
func channelPair(t *testing.T) (client, node *Channel) {
	t.Helper()
	nodeKey, _ := e2e.GenerateKeyPair()
	clientKey, _ := e2e.GenerateKeyPair()
	prologue := []byte("argus-e2e/v1|chan-7")
	init, msg1, err := e2e.NewInitiator(clientKey, nodeKey.Public, prologue)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	nodeSess, _, msg2, err := e2e.Respond(nodeKey, prologue, msg1)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	clientSess, err := init.Finish(msg2)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return NewChannel("chan-7", clientSess), NewChannel("chan-7", nodeSess)
}

func TestChannelSealRequestHidesParamsExposesRoute(t *testing.T) {
	client, node := channelPair(t)
	id := json.RawMessage("42")
	params := json.RawMessage(`{"session_id":"default:%3","text":"my-secret-input"}`)

	frame, err := client.sealRequest(&id, "sessions.input", "home", params)
	if err != nil {
		t.Fatalf("sealRequest: %v", err)
	}
	// Cleartext routing is present for the gateway...
	if frame.Method != "sessions.input" || frame.Route == nil ||
		frame.Route.ChanID != "chan-7" || frame.Route.NodeID != "home" {
		t.Fatalf("routing header wrong: method=%q route=%+v", frame.Method, frame.Route)
	}
	// ...but the sensitive payload is opaque: params must not appear on the wire.
	wire, _ := json.Marshal(frame)
	if strings.Contains(string(wire), "my-secret-input") || strings.Contains(string(wire), "session_id") {
		t.Fatalf("plaintext params leaked on the wire: %s", wire)
	}

	// The node opens it back to the exact params.
	got, err := node.OpenParams(RelayFrame{Body: frame.Body})
	if err != nil {
		t.Fatalf("openParams: %v", err)
	}
	if string(got) != string(params) {
		t.Errorf("opened params = %s, want %s", got, params)
	}
}

func TestChannelNotificationRoundTripKeepsSubIDCleartext(t *testing.T) {
	client, node := channelPair(t)
	params := json.RawMessage(`{"delta":"hidden text"}`)
	frame, err := client.sealNotification("transcript.delta", RouteHeader{SubID: "s-1"}, params)
	if err != nil {
		t.Fatalf("sealNotification: %v", err)
	}
	if frame.ID != nil {
		t.Error("notification frame must have no ID")
	}
	if frame.Route == nil || frame.Route.ChanID != "chan-7" || frame.Route.SubID != "s-1" {
		t.Fatalf("route = %+v", frame.Route)
	}
	got, err := node.OpenParams(RelayFrame{Body: frame.Body})
	if err != nil {
		t.Fatalf("openParams: %v", err)
	}
	if string(got) != string(params) {
		t.Errorf("opened = %s, want %s", got, params)
	}
}

func TestChannelWrongSessionFailsToOpen(t *testing.T) {
	client, _ := channelPair(t)
	_, other := channelPair(t) // an unrelated session
	id := json.RawMessage("1")
	frame, err := client.sealRequest(&id, "ping", "home", nil)
	if err != nil {
		t.Fatalf("sealRequest: %v", err)
	}
	if _, err := other.OpenParams(RelayFrame{Body: frame.Body}); err == nil {
		t.Fatal("opening with the wrong session must fail")
	}
}

func TestChannelResponseResultRoundTrip(t *testing.T) {
	node, client := channelPair(t) // seal on node side, open on client side
	id := json.RawMessage("42")
	result := json.RawMessage(`{"ok":true,"path":"/secret/dir"}`)

	frame, err := node.sealResponse(&id, result, nil)
	if err != nil {
		t.Fatalf("sealResponse: %v", err)
	}
	if frame.Route == nil || frame.Route.ChanID != "chan-7" {
		t.Fatalf("route = %+v", frame.Route)
	}
	wire, _ := json.Marshal(frame)
	if strings.Contains(string(wire), "/secret/dir") {
		t.Fatalf("result leaked on the wire: %s", wire)
	}
	gotResult, gotErr, err := client.OpenResponse(RelayFrame{Body: frame.Body})
	if err != nil {
		t.Fatalf("openResponse: %v", err)
	}
	if gotErr != nil {
		t.Fatalf("unexpected rpc error: %v", gotErr)
	}
	if string(gotResult) != string(result) {
		t.Errorf("result = %s, want %s", gotResult, result)
	}
}

func TestChannelResponseErrorRoundTripStaysSealed(t *testing.T) {
	node, client := channelPair(t)
	id := json.RawMessage("7")
	rpcErr := &RPCError{Code: CodeInternalError, Message: "sensitive failure detail"}

	frame, err := node.sealResponse(&id, nil, rpcErr)
	if err != nil {
		t.Fatalf("sealResponse: %v", err)
	}
	// The error must be sealed, not in the cleartext top-level Error.
	if frame.Error != nil {
		t.Error("node error must be sealed in Body, not the cleartext Error field")
	}
	wire, _ := json.Marshal(frame)
	if strings.Contains(string(wire), "sensitive failure detail") {
		t.Fatalf("error message leaked on the wire: %s", wire)
	}
	_, gotErr, err := client.OpenResponse(RelayFrame{Body: frame.Body})
	if err != nil {
		t.Fatalf("openResponse: %v", err)
	}
	if gotErr == nil || gotErr.Message != "sensitive failure detail" || gotErr.Code != CodeInternalError {
		t.Errorf("opened rpc error = %+v", gotErr)
	}
}

func TestChannelOpenRejectsMalformedBody(t *testing.T) {
	_, node := channelPair(t)
	cases := []struct {
		name string
		body json.RawMessage
	}{
		{"nil body", nil},
		{"json null", json.RawMessage(`null`)},
		{"object body", json.RawMessage(`{"x":1}`)},
		{"number body", json.RawMessage(`42`)},
		{"bad base64", json.RawMessage(`"not!!base64"`)},
		{"empty string", json.RawMessage(`""`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := RelayFrame{Body: tc.body}
			if _, err := node.OpenParams(frame); err == nil {
				t.Errorf("OpenParams(%s) must return an error, not succeed or panic", tc.name)
			}
			if _, _, err := node.OpenResponse(frame); err == nil {
				t.Errorf("OpenResponse(%s) must return an error, not succeed or panic", tc.name)
			}
		})
	}
}

func TestExportedFrameHelpersRoundTrip(t *testing.T) {
	client, node := channelPair(t) // paired sessions (client, node)

	// client seals a request -> node opens params
	id := json.RawMessage("1")
	params := json.RawMessage(`{"x":1}`)
	reqBytes, err := client.SealRequestFrame(&id, "test.m", "n1", params)
	if err != nil {
		t.Fatalf("SealRequestFrame: %v", err)
	}
	reqFrame := parseRelayFrame(t, reqBytes)
	gotParams, err := node.OpenParams(reqFrame)
	if err != nil || string(gotParams) != string(params) {
		t.Fatalf("OpenParams = %s err=%v", gotParams, err)
	}

	// node seals a response -> client opens it
	respBytes, err := node.SealResponseFrame(&id, json.RawMessage(`{"ok":true}`), nil)
	if err != nil {
		t.Fatalf("SealResponseFrame: %v", err)
	}
	res, rpcErr, err := client.OpenResponse(parseRelayFrame(t, respBytes))
	if err != nil || rpcErr != nil || string(res) != `{"ok":true}` {
		t.Fatalf("OpenResponse res=%s rpcErr=%v err=%v", res, rpcErr, err)
	}

	// node seals a notification -> client opens params
	notifBytes, err := node.SealNotificationFrame("test.note", RouteHeader{}, json.RawMessage(`{"n":2}`))
	if err != nil {
		t.Fatalf("SealNotificationFrame: %v", err)
	}
	nf := parseRelayFrame(t, notifBytes)
	if nf.Method != "test.note" {
		t.Errorf("notification method = %q", nf.Method)
	}
	np, err := client.OpenParams(nf)
	if err != nil || string(np) != `{"n":2}` {
		t.Fatalf("notification OpenParams = %s err=%v", np, err)
	}
}

func TestHandshakeFrameRoundTrip(t *testing.T) {
	if p := ChannelPrologue("n1", "c1"); string(p) != "argus-e2e/v1|n1|c1" {
		t.Errorf("prologue = %q", p)
	}
	raw, err := MarshalHandshakeFrame("c1", []byte{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("MarshalHandshakeFrame: %v", err)
	}
	f := parseRelayFrame(t, raw)
	if f.Method != MethodE2EHandshake || f.Route.ChanID != "c1" {
		t.Fatalf("handshake frame = method %q route %+v", f.Method, f.Route)
	}
	hs, err := HandshakeFromFrame(f)
	if err != nil || string(hs) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("HandshakeFromFrame = %v err=%v", hs, err)
	}
}

// parseRelayFrame decodes wire frame bytes into a RelayFrame (test helper).
func parseRelayFrame(t *testing.T, raw []byte) RelayFrame {
	t.Helper()
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if m.Route == nil {
		t.Fatalf("frame has no route: %s", raw)
	}
	return RelayFrame{Method: m.Method, ID: m.ID, Route: *m.Route, Body: m.Body, Raw: raw}
}
