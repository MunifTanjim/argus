package client

import (
	"encoding/json"
	"log"
	"sort"
	"sync"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

const botUnreachable = "unreachable"

// botOrigin composites a bot's id and stamps its node.
func (m *E2EClient) botOrigin(a api.BotInfo, nodeID string) api.BotInfo {
	a.ID = session.CompositeID(nodeID, a.ID)
	a.NodeID = nodeID
	a.NodeLabel = m.nodeLabel(nodeID)
	return a
}

// fanoutBots merges every node's bots with composite ids, ordered by node
// id. Bots last seen on a node with no channel, or that failed to answer, are
// included with status "unreachable" until the node answers again or is gone.
// Each node has its own primary; bots keep the Primary flag their node set.
// The merged result's Primary is left empty: it names one bot, not one per node.
func (m *E2EClient) fanoutBots(raw json.RawMessage, out any) error {
	chans := m.channelsSnapshot()
	sort.Slice(chans, func(i, j int) bool { return chans[i].nodeID < chans[j].nodeID })
	type res struct {
		bots  []api.BotInfo
		ok    bool
		epoch uint64
	}
	results := make([]res, len(chans))
	var wg sync.WaitGroup
	for i, nc := range chans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.mu.Lock()
			epoch := m.botEpoch[nc.nodeID]
			m.mu.Unlock()
			var r api.BotListResult
			err := m.callNode(nc.nodeID, api.MethodBotList, raw, &r)
			if m.botListHook != nil {
				m.botListHook(nc.nodeID)
			}
			if err != nil {
				log.Printf("client: warn: bot.list on node %s failed: %v", nc.nodeID, err)
				return
			}
			bots := make([]api.BotInfo, len(r.Bots))
			for j, a := range r.Bots {
				bots[j] = m.botOrigin(a, nc.nodeID)
				bots[j].NodeLabel = nc.label
			}
			results[i] = res{bots: bots, ok: true, epoch: epoch}
		}()
	}
	wg.Wait()

	m.mu.Lock()
	byNode := map[string][]api.BotInfo{}
	online := map[string]bool{}
	for i, r := range results {
		nodeID := chans[i].nodeID
		online[nodeID] = true
		// A node lost while its list was in flight bumped the epoch; its
		// reply is stale and must not replace the cache.
		if r.ok && m.botEpoch[nodeID] == r.epoch && m.byNode[nodeID] != nil {
			m.botCache[nodeID] = r.bots
			byNode[nodeID] = r.bots
			continue
		}
		byNode[nodeID] = unreachableBots(m.botCache[nodeID])
	}
	for nodeID, cached := range m.botCache {
		if !online[nodeID] {
			byNode[nodeID] = unreachableBots(cached)
		}
	}
	m.mu.Unlock()

	nodeIDs := make([]string, 0, len(byNode))
	for id := range byNode {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	merged := []api.BotInfo{}
	for _, id := range nodeIDs {
		merged = append(merged, byNode[id]...)
	}
	return assign(out, api.BotListResult{Bots: merged})
}

func unreachableBots(in []api.BotInfo) []api.BotInfo {
	out := make([]api.BotInfo, len(in))
	for i, a := range in {
		a.Status = botUnreachable
		out[i] = a
	}
	return out
}

func botIDEmpty(raw json.RawMessage) bool {
	id, _ := stringField(raw, "bot_id")
	return id == ""
}

func splitIDField(raw json.RawMessage, field, what string) (nodeID string, params json.RawMessage, err error) {
	composite, err := stringField(raw, field)
	if err != nil {
		return "", nil, err
	}
	nodeID, local, ok := session.SplitCompositeID(composite)
	if !ok {
		return "", nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: what + " id is not gateway-qualified: " + composite}
	}
	params, err = setStringField(raw, field, local)
	return nodeID, params, err
}

// routeByBot splits the composite bot_id, routes to its node, and
// composites node-local ids in the result.
func (m *E2EClient) routeByBot(method string, raw json.RawMessage, out any) error {
	nodeID, params, err := splitIDField(raw, "bot_id", "bot")
	if err != nil {
		return err
	}
	if method == api.MethodBotThreadSend || method == api.MethodBotMemoryUndo {
		if tid, _ := stringField(raw, "thread_id"); tid != "" {
			tnode, tlocal, ok := session.SplitCompositeID(tid)
			if !ok || tnode != nodeID {
				return &api.RPCError{Code: api.CodeInvalidRequest, Message: "thread " + tid + " does not belong to the bot's node"}
			}
			if params, err = setStringField(params, "thread_id", tlocal); err != nil {
				return err
			}
		}
	}
	switch method {
	case api.MethodBotShow, api.MethodBotRename:
		var info api.BotInfo
		if err := m.callNode(nodeID, method, params, &info); err != nil {
			return err
		}
		return assign(out, m.botOrigin(info, nodeID))
	case api.MethodBotThreadSend:
		var r api.BotSendResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		r.ThreadID = session.CompositeID(nodeID, r.ThreadID)
		if r.SessionID != "" {
			r.SessionID = session.CompositeID(nodeID, r.SessionID)
		}
		return assign(out, r)
	case api.MethodBotThreadList:
		var r api.BotThreadsResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Threads {
			t := &r.Threads[i]
			t.ID = session.CompositeID(nodeID, t.ID)
			t.BotID = session.CompositeID(nodeID, t.BotID)
			if t.SessionID != "" {
				t.SessionID = session.CompositeID(nodeID, t.SessionID)
			}
		}
		return assign(out, r)
	case api.MethodBotMemoryLog:
		var r api.BotMemoryLogResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Commits {
			if r.Commits[i].ThreadID != "" {
				r.Commits[i].ThreadID = session.CompositeID(nodeID, r.Commits[i].ThreadID)
			}
		}
		return assign(out, r)
	case api.MethodBotActivity:
		var r api.BotActivityResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Activity {
			a := &r.Activity[i]
			if a.ThreadID != "" {
				a.ThreadID = session.CompositeID(nodeID, a.ThreadID)
			}
			if a.SessionID != "" {
				a.SessionID = session.CompositeID(nodeID, a.SessionID)
			}
		}
		return assign(out, r)
	case api.MethodBotMemoryProposals:
		var r api.BotMemoryProposalsResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Proposals {
			pr := &r.Proposals[i]
			pr.ID = session.CompositeID(nodeID, pr.ID)
			pr.BotID = session.CompositeID(nodeID, pr.BotID)
			if pr.ThreadID != "" {
				pr.ThreadID = session.CompositeID(nodeID, pr.ThreadID)
			}
		}
		return assign(out, r)
	case api.MethodBotRoutineList:
		var r api.BotRoutineListResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Routines {
			r.Routines[i].BotID = session.CompositeID(nodeID, r.Routines[i].BotID)
		}
		return assign(out, r)
	case api.MethodBotUsage:
		var r api.BotUsageResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		r.BotID = session.CompositeID(nodeID, r.BotID)
		return assign(out, r)
	case api.MethodBotRoutinePause, api.MethodBotRoutineResume:
		var r api.BotRoutineInfo
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		r.BotID = session.CompositeID(nodeID, r.BotID)
		return assign(out, r)
	case api.MethodBotRoutineRuns:
		var r api.BotRoutineRunsResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Runs {
			if r.Runs[i].ThreadID != "" {
				r.Runs[i].ThreadID = session.CompositeID(nodeID, r.Runs[i].ThreadID)
			}
		}
		return assign(out, r)
	}
	return m.callNode(nodeID, method, params, out)
}

// routeByBotProposal splits the composite proposal_id and routes to its node.
func (m *E2EClient) routeByBotProposal(method string, raw json.RawMessage, out any) error {
	nodeID, params, err := splitIDField(raw, "proposal_id", "proposal")
	if err != nil {
		return err
	}
	return m.callNode(nodeID, method, params, out)
}

// routeByBotThread splits the composite thread_id and routes to its node.
func (m *E2EClient) routeByBotThread(method string, raw json.RawMessage, out any) error {
	nodeID, params, err := splitIDField(raw, "thread_id", "thread")
	if err != nil {
		return err
	}
	if method == api.MethodBotOfferStart {
		var r api.BotSendResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		r.ThreadID = session.CompositeID(nodeID, r.ThreadID)
		if r.SessionID != "" {
			r.SessionID = session.CompositeID(nodeID, r.SessionID)
		}
		return assign(out, r)
	}
	if method != api.MethodBotThreadTimeline {
		return m.callNode(nodeID, method, params, out)
	}
	var tl api.BotTimeline
	if err := m.callNode(nodeID, method, params, &tl); err != nil {
		return err
	}
	tl.ThreadID = session.CompositeID(nodeID, tl.ThreadID)
	for i := range tl.Sessions {
		if tl.Sessions[i].SessionID != "" {
			tl.Sessions[i].SessionID = session.CompositeID(nodeID, tl.Sessions[i].SessionID)
		}
	}
	return assign(out, tl)
}
