package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// relayPair connects a fake node (id n1) and a fake client to srv and opens a
// relay channel between them.
func relayPair(t *testing.T, srv *Server) (client, node *api.Peer, chanID string, fromClient, fromNode chan api.RelayFrame) {
	t.Helper()
	fromClient = make(chan api.RelayFrame, 8)
	fromNode = make(chan api.RelayFrame, 8)

	gwNodeConn, nodeConn := net.Pipe()
	node = api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return api.IdentifyResult{ID: "n1", Label: "n1"}, nil
			}
			return nil, nil
		},
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) { fromClient <- f },
	})
	go srv.serveNode(gwNodeConn)
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.nodePeers["n1"] != nil
	})

	gwClientConn, appConn := net.Pipe()
	go srv.clientSrv.ServeConnContext(context.Background(), gwClientConn)
	client = api.NewPeer(appConn, api.PeerOptions{
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) { fromNode <- f },
	})
	t.Cleanup(func() { client.Close(); node.Close(); gwNodeConn.Close(); gwClientConn.Close() })

	var open api.RelayOpenResult
	if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &open); err != nil {
		t.Fatalf("relay.open: %v", err)
	}
	return client, node, open.ChanID, fromClient, fromNode
}

func awaitRelay(t *testing.T, ch chan api.RelayFrame) api.RelayFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no relay frame forwarded")
		return api.RelayFrame{}
	}
}

func TestGatewayForwardsCBORUnchanged(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	client, node, chanID, fromClient, fromNode := relayPair(t, srv)
	ch := api.NewPlainChannel(chanID)

	params, _ := api.CBORMarshal(api.TerminalInputParams{TermID: "t1", Data: []byte{0, 1, 0xff}})
	id := json.RawMessage("7")
	req, err := ch.SealRequestFrame(api.EncodingCBOR, &id, api.MethodTerminalInput, "n1", params)
	if err != nil {
		t.Fatalf("SealRequestFrame: %v", err)
	}
	if err := client.SendRawFrame(api.EncodingCBOR, req); err != nil {
		t.Fatalf("send: %v", err)
	}
	if f := awaitRelay(t, fromClient); f.Enc != api.EncodingCBOR || !bytes.Equal(f.Raw, req) {
		t.Fatalf("client->node frame enc=%v raw=%x, want CBOR %x", f.Enc, f.Raw, req)
	}

	out, _ := api.CBORMarshal(api.TerminalOutput{TermID: "t1", Data: []byte("hi\x00")})
	note, err := ch.SealNotificationFrame(api.EncodingCBOR, api.MethodTerminalOutput, api.RouteHeader{}, out)
	if err != nil {
		t.Fatalf("SealNotificationFrame: %v", err)
	}
	if err := node.SendRawFrame(api.EncodingCBOR, note); err != nil {
		t.Fatalf("send: %v", err)
	}
	if f := awaitRelay(t, fromNode); f.Enc != api.EncodingCBOR || !bytes.Equal(f.Raw, note) {
		t.Fatalf("node->client frame enc=%v raw=%x, want CBOR %x", f.Enc, f.Raw, note)
	}
}

func TestGatewayServerInfoReportsCBOR(t *testing.T) {
	s := NewServer(New(0), nil, nil)
	res, err := s.clientSrv.DispatchFunc()(context.Background(), api.MethodServerInfo, nil)
	if err != nil {
		t.Fatalf("server.info: %v", err)
	}
	b, _ := json.Marshal(res)
	var info struct {
		Capabilities *struct {
			CBOR bool `json:"cbor"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(b, &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Capabilities == nil || !info.Capabilities.CBOR {
		t.Fatalf("server.info = %s, want capabilities.cbor true", b)
	}
}

func TestGatewayIdentifyDeclaresCBOR(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	got := make(chan json.RawMessage, 1)
	gwConn, nodeConn := net.Pipe()
	defer gwConn.Close()
	node := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				got <- params
				return api.IdentifyResult{ID: "n1", Label: "n1"}, nil
			}
			return nil, nil
		},
	})
	defer node.Close()
	go srv.serveNode(gwConn)

	select {
	case params := <-got:
		var p api.IdentifyParams
		if err := json.Unmarshal(params, &p); err != nil || !p.Capabilities.CBOR {
			t.Fatalf("node.identify params = %s (err %v), want capabilities.cbor true", params, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway never called node.identify")
	}
}
