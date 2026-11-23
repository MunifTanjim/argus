package node

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/MunifTanjim/argus/internal/api"
)

// botRouted wraps a bot.* handler so a request whose node field names
// another node is forwarded there over the peer channel, without the field.
// A request that came from a peer node is never forwarded again.
func (d *Node) botRouted(method string, h api.HandlerFunc) api.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(params, &fields) != nil {
			return h(ctx, params)
		}
		var ref string
		if raw, ok := fields[api.BotNodeField]; !ok || json.Unmarshal(raw, &ref) != nil || ref == "" {
			return h(ctx, params)
		}
		node, err := d.resolveNode(ref)
		if err != nil {
			return nil, botErr("%v", err)
		}
		if node == d.id {
			return h(ctx, params)
		}
		if _, onPeer := peerNodeFrom(ctx); onPeer {
			return nil, botErr("%s for node %s was sent to node %s", method, node, d.id)
		}
		delete(fields, api.BotNodeField)
		var out json.RawMessage
		if err := d.forward(ctx, node, method, fields, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// botListAll lists this node's bots, then those of every online peer in node
// id order, each stamped with its node's label. A peer that lacks
// cross-node support, errors, or is slow is reported in Unreachable; one with
// bots disabled has no bots to list.
func (d *Node) botListAll(ctx context.Context, p api.BotListParams) (api.BotListResult, error) {
	peers := d.roster.others()
	slices.SortFunc(peers, func(x, y api.NodeDescriptor) int { return strings.Compare(x.ID, y.ID) })
	results := make([][]api.BotInfo, len(peers))
	failed := make([]bool, len(peers))
	var wg sync.WaitGroup
	for i, n := range peers {
		if !n.Online || n.Capabilities.CrossNodeOrch < 1 {
			failed[i] = true
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, listPeerTimeout)
			defer cancel()
			var res api.BotListResult
			err := d.forward(cctx, n.ID, api.MethodBotList, api.BotListParams{IncludeArchived: p.IncludeArchived}, &res)
			if err != nil {
				var rpcErr *api.RPCError
				failed[i] = !errors.As(err, &rpcErr) || rpcErr.Message != errBotsDisabled
				return
			}
			for j := range res.Bots {
				res.Bots[j].NodeID, res.Bots[j].NodeLabel = n.ID, n.Label
			}
			results[i] = res.Bots
		}()
	}
	local, err := d.botListLocal(ctx, p)
	wg.Wait()
	out := api.BotListResult{Bots: []api.BotInfo{}}
	if err == nil {
		for _, a := range local.Bots {
			a.NodeLabel = d.label
			out.Bots = append(out.Bots, a)
		}
	} else if d.bots != nil {
		return api.BotListResult{}, err
	}
	for i, n := range peers {
		if failed[i] {
			out.Unreachable = append(out.Unreachable, n.ID)
			continue
		}
		out.Bots = append(out.Bots, results[i]...)
	}
	return out, nil
}
