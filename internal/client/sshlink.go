package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/sshconn"
)

const viaSSH = "ssh"

// defaultSSHCommand is the remote command when P2POptions.SSHCommand is empty.
const defaultSSHCommand = "argus bridge"

// sshDial runs the remote command over ssh and returns its stdio.
var sshDial = sshconn.DialCommand

// sshTargetFor returns the p2p.ssh target configured for nd, by id then by
// label (config keys are lowercased by the loader). A label several known
// nodes share is skipped with a warning.
func (m *E2EClient) sshTargetFor(nd api.NodeDescriptor) (string, bool) {
	m.mu.Lock()
	targets := m.p2p.SSH
	shared := 0
	if nd.Label != "" {
		for _, d := range m.desc {
			if strings.EqualFold(d.Label, nd.Label) {
				shared++
			}
		}
	}
	m.mu.Unlock()
	for k, v := range targets {
		if strings.EqualFold(k, nd.ID) {
			return v, true
		}
	}
	if nd.Label == "" {
		return "", false
	}
	for k, v := range targets {
		if !strings.EqualFold(k, nd.Label) {
			continue
		}
		if shared > 1 {
			m.logOnce("ssh-label\x00"+k, "client: p2p.ssh %q matches the label of several nodes; skipping it", k)
			return "", false
		}
		return v, true
	}
	return "", false
}

// trySSH dials nd over its configured ssh target and switches the node onto
// the new link from the relay or tcp. It returns the link, or nil when the
// node stays where it is.
func (m *E2EClient) trySSH(nd api.NodeDescriptor) *nodeChan {
	target, ok := m.sshTargetFor(nd)
	if !ok {
		return nil
	}
	nc, err := m.dialSSH(nd, target)
	if err != nil {
		m.logOnce(nd.ID+"\x00ssh\x00"+err.Error(), "client: node %s ssh link unavailable: %v", nd.ID, err)
		return nil
	}
	if err := m.swapLink(nd.ID, nc); err != nil {
		return nil
	}
	return nc
}

// dialSSH runs the bridge command on target and identifies the node over the
// node's local-socket protocol. SSH host keys authenticate the node; identify
// only checks it is the node expected. The link takes over the identity key
// of the relay link it replaces. The node is not switched onto it.
func (m *E2EClient) dialSSH(nd api.NodeDescriptor, target string) (_ *nodeChan, err error) {
	dest, port, err := sshconn.ParseSSHTarget(target)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	cmd := strings.Fields(m.p2p.SSHCommand)
	cur := m.byNode[nd.ID]
	m.mu.Unlock()
	if cur == nil || (cur.via != viaGateway && cur.via != viaTCP) {
		return nil, errors.New("node is not on the relay or tcp")
	}
	if len(cmd) == 0 {
		cmd = strings.Fields(defaultSSHCommand)
	}
	conn, err := sshDial(dest, port, cmd, slog.Default())
	if err != nil {
		return nil, err
	}
	nc := &nodeChan{
		nodeID:      nd.ID,
		label:       nd.Label,
		identityPub: cur.identityPub,
		standby:     true,
		replaces:    cur,
		via:         viaSSH,
		done:        make(chan struct{}),
	}
	peer := api.NewPeer(conn, api.PeerOptions{
		OnNotify:                  func(n api.Notification) { m.deliverNote(nc, n) },
		KeepaliveInterval:         p2pKeepaliveInterval,
		KeepaliveTimeout:          p2pKeepaliveTimeout,
		KeepaliveFailureThreshold: p2pKeepaliveFailures,
	})
	nc.out = peer
	var once sync.Once
	nc.close = func() {
		once.Do(func() {
			m.unregister(nc, "")
			go peer.Close()
		})
	}
	defer func() {
		if err != nil {
			nc.close()
		}
	}()
	ctx, cancel := context.WithTimeout(m.trustCtx, time.Duration(handshakeTimeoutNs.Load()))
	defer cancel()
	var res api.IdentifyResult
	if err := peer.CallContext(ctx, api.MethodNodeIdentify, api.IdentifyParams{Capabilities: &api.PeerCapabilities{CBOR: true}}, &res); err != nil {
		return nil, fmt.Errorf("identify: %w", err)
	}
	if res.ID != nd.ID {
		return nil, fmt.Errorf("ssh target is node %q, not %q", res.ID, nd.ID)
	}
	if !m.plaintext && res.IdentityPubKey != nd.IdentityPubKey {
		return nil, errors.New("ssh target's identity key does not match the roster")
	}
	// No gateway on this link: the node's declaration alone decides CBOR.
	peer.SetRemoteCBOR(res.Capabilities.CBOR)
	nc.cbor.Store(res.Capabilities.CBOR)
	if !m.plaintext {
		m.mu.Lock()
		m.authTip[string(nc.identityPub)] = res.Tip
		m.mu.Unlock()
	}
	return nc, nil
}

// callSSH is callChan on an SSH link: plain JSON-RPC on the node's local-socket
// protocol. A call fails at once when the link closes.
func (m *E2EClient) callSSH(nc *nodeChan, method string, params, out any, inline func(pendingReply)) error {
	nc.calls.Add(1)
	defer nc.calls.Add(-1)
	ctx, cancel := context.WithTimeout(context.Background(), callTimeoutFor(method))
	defer cancel()
	go func() {
		select {
		case <-nc.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	var hook func(api.Encoding, json.RawMessage, *api.RPCError)
	if inline != nil {
		hook = func(enc api.Encoding, result json.RawMessage, rpcErr *api.RPCError) {
			inline(pendingReply{result: result, rpcErr: rpcErr, enc: enc})
		}
	}
	err := nc.out.CallContextInline(ctx, method, params, out, hook)
	if err != nil && ctx.Err() != nil {
		select {
		case <-nc.done:
			return fmt.Errorf("client: link to node %q closed", nc.nodeID)
		default:
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("client: call timeout")
		}
	}
	return err
}
