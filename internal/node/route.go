package node

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// ownerOf returns the node that owns a composite session or message ID. The ID
// is remote only when its prefix is another node in the roster; any other ID,
// including a bare one that looks composite like default:%3, is local.
func (d *Node) ownerOf(id string) (nodeID string, remote bool) {
	node, _, ok := session.SplitCompositeID(id)
	if !ok || node == d.id {
		return d.id, false
	}
	if _, known := d.roster.get(node); !known {
		return d.id, false
	}
	return node, true
}

// resolveNode returns the ID of the node ref names: this node when ref is
// empty, else the node whose ID is ref, else the one node labeled ref.
func (d *Node) resolveNode(ref string) (string, error) {
	if ref == "" || ref == d.id {
		return d.id, nil
	}
	if _, ok := d.roster.get(ref); ok {
		return ref, nil
	}
	ids := d.roster.withLabel(ref)
	if ref == d.label && !slices.Contains(ids, d.id) {
		ids = append(ids, d.id)
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("unknown node: %s", ref)
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("node label %q is ambiguous: %s", ref, strings.Join(ids, ", "))
	}
}

// forward sends an orch.* call to nodeID over this node's peer channel.
func (d *Node) forward(ctx context.Context, nodeID, method string, params, out any) error {
	n, ok := d.roster.get(nodeID)
	if !ok || !n.Online {
		return offline(nodeID)
	}
	if n.Capabilities.CrossNodeOrch < 1 {
		return fmt.Errorf("node %s does not support cross-node orchestration", nodeID)
	}
	return d.peers.Load().call(ctx, nodeID, method, params, out)
}

// routed forwards method to the node that owns id when that is another node,
// and reports whether it did. A call that came from a peer node is never
// forwarded again: that node sends only IDs it believes this node owns.
func routed[R any](ctx context.Context, d *Node, id, method string, params any) (res R, done bool, err error) {
	node, remote := d.ownerOf(id)
	if !remote {
		return res, false, nil
	}
	if _, onPeer := peerNodeFrom(ctx); onPeer {
		return res, true, fmt.Errorf("%s is not on node %s", id, d.id)
	}
	return res, true, d.forward(ctx, node, method, params, &res)
}

// routedMessage is routed for a message ID. Message IDs always carry the node
// that created them, so an ID whose node is not in the roster belongs to a
// node that is not connected, never to this one.
func routedMessage[R any](ctx context.Context, d *Node, id, method string, params any) (res R, done bool, err error) {
	if node, _, ok := session.SplitCompositeID(id); ok && node != d.id {
		if _, known := d.roster.get(node); !known {
			return res, true, fmt.Errorf("%s is on node %s, which is not connected", id, node)
		}
	}
	return routed[R](ctx, d, id, method, params)
}

// remote is the caller as the target node receives it.
func (c callerRef) remote() api.CallerInfo {
	return api.CallerInfo{Remote: &api.RemoteCaller{Actor: c.Actor, SessionID: c.SessionID, Label: c.Label}}
}
