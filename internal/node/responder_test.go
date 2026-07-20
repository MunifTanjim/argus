package node

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func newE2ETestNode(t *testing.T) *Node {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetIdentity("home", "home-box")
	kp, _ := e2e.GenerateKeyPair()
	d.SetIdentityKey(kp)
	return d
}

// e2eNodePair connects a fake client to a node responder over net.Pipe (standing
// in for the gateway relay) and completes the Noise handshake. It returns the
// client Channel, the client Peer (to send sealed frames), a channel of relay
// frames the node sends back, and a cleanup func.
func e2eNodePair(t *testing.T, d *Node) (cch *api.Channel, client *api.Peer, fromNode chan api.RelayFrame, cleanup func()) {
	t.Helper()
	kpClient, _ := e2e.GenerateKeyPair()
	clientConn, nodeConn := net.Pipe()

	resp := d.newRelayResponder()
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{OnRelayFrame: resp.onFrame})
	resp.peer.Store(nodePeer)

	fromNode = make(chan api.RelayFrame, 32)
	client = api.NewPeer(clientConn, api.PeerOptions{OnRelayFrame: func(f api.RelayFrame) { fromNode <- f }})

	init, msg1, err := e2e.NewInitiator(kpClient, d.identity.Public, api.ChannelPrologue(d.id, "c1"))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	hf, _ := api.MarshalHandshakeFrame("c1", msg1)
	if err := client.SendRawFrame(hf); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	select {
	case f := <-fromNode:
		msg2, err := api.HandshakeFromFrame(f)
		if err != nil {
			t.Fatalf("HandshakeFromFrame: %v", err)
		}
		sess, err := init.Finish(msg2)
		if err != nil {
			t.Fatalf("Finish: %v", err)
		}
		cch = api.NewChannel("c1", sess)
	case <-time.After(2 * time.Second):
		t.Fatal("no handshake reply from node")
	}
	cleanup = func() { client.Close(); nodePeer.Close(); resp.closeAll() }
	return cch, client, fromNode, cleanup
}

func TestResponderRequestResponse(t *testing.T) {
	d := newE2ETestNode(t)
	d.server.Handle("test.echo", func(_ context.Context, params json.RawMessage) (any, error) {
		return params, nil
	})
	cch, client, fromNode, cleanup := e2eNodePair(t, d)
	defer cleanup()

	id := json.RawMessage("1")
	params := json.RawMessage(`{"hello":"world"}`)
	req, _ := cch.SealRequestFrame(&id, "test.echo", "home", params)
	if err := client.SendRawFrame(req); err != nil {
		t.Fatalf("send req: %v", err)
	}
	select {
	case f := <-fromNode:
		res, rpcErr, err := cch.OpenResponse(f)
		if err != nil || rpcErr != nil {
			t.Fatalf("OpenResponse err=%v rpcErr=%v", err, rpcErr)
		}
		if string(res) != string(params) {
			t.Errorf("echo = %s, want %s", res, params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no response from node")
	}
}

func TestResponderErrorResponse(t *testing.T) {
	d := newE2ETestNode(t)
	d.server.Handle("test.fail", func(context.Context, json.RawMessage) (any, error) {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "boom detail"}
	})
	cch, client, fromNode, cleanup := e2eNodePair(t, d)
	defer cleanup()

	id := json.RawMessage("2")
	req, _ := cch.SealRequestFrame(&id, "test.fail", "home", nil)
	client.SendRawFrame(req)
	select {
	case f := <-fromNode:
		if bytes.Contains(f.Raw, []byte("boom detail")) {
			t.Fatal("error detail leaked in cleartext")
		}
		_, rpcErr, err := cch.OpenResponse(f)
		if err != nil {
			t.Fatalf("OpenResponse: %v", err)
		}
		if rpcErr == nil || rpcErr.Message != "boom detail" {
			t.Errorf("rpc error = %+v", rpcErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no error response")
	}
}

func TestResponderStreamsNotifications(t *testing.T) {
	d := newE2ETestNode(t)
	d.server.Handle("test.sub", func(ctx context.Context, _ json.RawMessage) (any, error) {
		n, ok := api.NotifierFrom(ctx)
		if !ok {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no notifier"}
		}
		go func() { _ = n.Notify("test.note", json.RawMessage(`{"n":7}`)) }()
		return nil, nil
	})
	cch, client, fromNode, cleanup := e2eNodePair(t, d)
	defer cleanup()

	id := json.RawMessage("3")
	req, _ := cch.SealRequestFrame(&id, "test.sub", "home", nil)
	client.SendRawFrame(req)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case f := <-fromNode:
			if f.Method == "test.note" {
				p, err := cch.OpenParams(f)
				if err != nil || string(p) != `{"n":7}` {
					t.Fatalf("notification params = %s err=%v", p, err)
				}
				return
			}
			// Response frame: a real client Opens every frame in arrival order to keep
			// the Noise dec-nonce in sync. Open and discard it.
			if _, _, err := cch.OpenResponse(f); err != nil {
				t.Fatalf("open response frame: %v", err)
			}
		case <-deadline:
			t.Fatal("no test.note notification streamed over the channel")
		}
	}
}
