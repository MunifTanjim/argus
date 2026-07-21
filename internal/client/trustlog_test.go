package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// trustGatewayConn is one end of a net.Pipe running a minimal gateway that answers
// nodes.list (empty) and trustlog.pull with the latest chain sent on chain.
func trustGatewayConn(t *testing.T, chain <-chan []byte) net.Conn {
	t.Helper()
	srvConn, cliConn := net.Pipe()
	var mu sync.Mutex
	var current []byte
	go func() {
		peer := api.NewPeer(srvConn, api.PeerOptions{
			Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
				switch method {
				case api.MethodNodesList:
					return api.NodesListResult{Nodes: []api.NodeDescriptor{}}, nil
				case api.MethodTrustLogPull:
					mu.Lock()
					select {
					case c := <-chain:
						current = c
					default:
					}
					cur := current
					mu.Unlock()
					return api.TrustLogChain{Chain: cur}, nil
				}
				return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
			},
		})
		<-peer.Done()
	}()
	return cliConn
}

func TestClientPullsAndReSyncsTrustLog(t *testing.T) {
	clientTrustSyncInterval.Store(int64(20 * time.Millisecond))
	t.Cleanup(func() { clientTrustSyncInterval.Store(int64(30 * time.Second)) })

	// Genesis-only chain first; an authorize appended later.
	signer, _ := trustlog.GenerateSigner()
	log, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	head := log.Head()
	genChain := trustlog.MarshalChain(log.Entries())
	device := bytes.Repeat([]byte{0x22}, 32)

	chainCh := make(chan []byte, 4)
	chainCh <- genChain
	conn := trustGatewayConn(t, chainCh)

	c, err := NewE2EClientWithGenesis(conn, head)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	waitClient(t, "genesis synced", func() bool { return c.TrustHead() != nil })
	if c.DeviceAuthorized(device) {
		t.Fatal("device not yet authorized")
	}

	// Append an authorize and let the periodic pull pick it up.
	_ = log.AuthorizeDevice(device, signer)
	chainCh <- trustlog.MarshalChain(log.Entries())
	waitClient(t, "device authorized after re-sync", func() bool { return c.DeviceAuthorized(device) })
}

func waitClient(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
