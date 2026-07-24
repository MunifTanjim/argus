package e2etest

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
)

// startLockNode builds a node with signer+identity keys and a trust chain path,
// uplinked to the gateway, serving its unix socket at socketPath. chainPath is
// where lock state is persisted; callers pass it in so they can call
// EnableTrustLog after lock.init distributes the genesis head.
func startLockNode(t *testing.T, ctx context.Context, id, gwURL, socketPath, chainPath string) *node.Node {
	t.Helper()
	n := node.New()
	n.SetIdentity(id, id)
	n.SetVersion("itest")
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("identity keypair: %v", err)
	}
	n.SetIdentityKey(kp)
	sk, err := node.LoadOrCreateSigner(filepath.Join(t.TempDir(), "signer-key.json"))
	if err != nil {
		t.Fatalf("signer keypair: %v", err)
	}
	n.SetSignerKey(sk)
	n.SetTrustChainPath(chainPath)
	go func() { _ = n.Run(ctx, socketPath) }()
	go n.ConnectGateway(ctx, wsURL(gwURL, "/node"), "", nil)
	return n
}

// resolveSignersForTest returns the base64-decoded SignerPubKey of each node in
// nodes whose ID matches one of the given ids (the caller's own key is
// auto-included by the lock.init handler).
func resolveSignersForTest(nodes []api.NodeDescriptor, ids ...string) ([][]byte, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out [][]byte
	for _, nd := range nodes {
		if !want[nd.ID] || nd.SignerPubKey == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(nd.SignerPubKey)
		if err != nil {
			return nil, fmt.Errorf("node %s: bad signer key: %w", nd.ID, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// gatherDevicesForTest returns the base64-decoded IdentityPubKey of every node
// that has one. These become the initially-authorized devices.
func gatherDevicesForTest(nodes []api.NodeDescriptor) [][]byte {
	var out [][]byte
	for _, nd := range nodes {
		if nd.IdentityPubKey == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
		if err != nil || len(b) != 32 {
			continue
		}
		out = append(out, b)
	}
	return out
}

func TestLockInitPropagatesThroughGateway(t *testing.T) {
	node.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	client.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	t.Cleanup(func() {
		node.SetTrustSyncIntervalForTest(30 * time.Second)
		client.SetTrustSyncIntervalForTest(30 * time.Second)
	})

	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	sockA := filepath.Join(dir, "a.sock")
	chainPathA := filepath.Join(dir, "chainA")
	chainPathB := filepath.Join(dir, "chainB")
	nodeA := startLockNode(t, ctx, "node-a", ts.URL, sockA, chainPathA)
	nodeB := startLockNode(t, ctx, "node-b", ts.URL, filepath.Join(dir, "b.sock"), chainPathB)

	// Wait until both nodes are on the roster with signer and identity keys.
	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "both nodes rostered with signer keys", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil || len(r.Nodes) != 2 {
			return false
		}
		for _, nd := range r.Nodes {
			if nd.SignerPubKey == "" || nd.IdentityPubKey == "" {
				return false
			}
		}
		return true
	})
	var roster api.NodesListResult
	if err := poll.Call(api.MethodNodesList, nil, &roster); err != nil {
		t.Fatalf("nodes.list: %v", err)
	}
	poll.Close()

	// Build signer + device lists from the roster. The client needs a stable
	// authorized key (node enforcement rejects ephemeral keys after lock.init).
	sigPubs, err := resolveSignersForTest(roster.Nodes, "node-b")
	if err != nil {
		t.Fatalf("resolve signers: %v", err)
	}
	devices := gatherDevicesForTest(roster.Nodes)
	clientKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}
	devices = append(devices, clientKP.Public)

	// Wait for node A's unix socket to be ready, then call lock.init on it.
	var aConn net.Conn
	waitFor(t, "node A socket ready", func() bool {
		c, derr := net.Dial("unix", sockA)
		if derr != nil {
			return false
		}
		aConn = c
		return true
	})
	ac := api.NewClient(aConn)
	var initRes api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{Signers: sigPubs, Devices: devices}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	ac.Close()
	if initRes.SignerCount != 2 {
		t.Fatalf("SignerCount = %d, want 2", initRes.SignerCount)
	}

	// Node A's trust store is now live. In production, the genesis head is
	// distributed to other nodes (e.g. via config or CLI output) so they can boot
	// locked. Simulate that here by enabling node B's trust store with the genesis
	// head; its sync loop then pulls the chain from the gateway.
	if err := nodeB.EnableTrustLog(initRes.Tip, chainPathB); err != nil {
		t.Fatalf("EnableTrustLog node B: %v", err)
	}

	// The chain propagates to node B via the gateway: node A offered it,
	// the gateway holds it, node B pulls and ingests it. Both nodes should
	// converge on the same current HEAD (which is the head AFTER device entries,
	// not the genesis head).
	waitFor(t, "node B converges on node A's head", func() bool {
		stB := nodeB.TrustStore()
		stA := nodeA.TrustStore()
		return stB != nil && stA != nil && bytes.Equal(stB.Tip(), stA.Tip())
	})

	// A genesis-pinned client pulls the chain from the gateway and sees node A
	// authorized. clientKP is included in the genesis devices so the now-locked
	// nodes accept its handshake.
	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClientLocked(ctx, dial, initRes.Tip, clientKP, "")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer c.Close()

	// Find node A's identity pubkey (an authorized device).
	var idA []byte
	for _, nd := range roster.Nodes {
		if nd.ID == "node-a" {
			idA, _ = base64.StdEncoding.DecodeString(nd.IdentityPubKey)
		}
	}
	if len(idA) == 0 {
		t.Fatal("node-a identity key not found in roster")
	}
	waitFor(t, "client sees node A authorized", func() bool { return c.DeviceAuthorized(idA) })
}
