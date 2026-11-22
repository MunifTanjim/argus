package client

import (
	"encoding/json"
	"log"
	"sort"
	"sync"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

const pagentUnreachable = "unreachable"

// pagentOrigin composites an agent's id and stamps its node.
func (m *E2EClient) pagentOrigin(a api.PAgentInfo, nodeID string) api.PAgentInfo {
	a.ID = session.CompositeID(nodeID, a.ID)
	a.NodeID = nodeID
	a.NodeLabel = m.nodeLabel(nodeID)
	return a
}

// fanoutPAgents merges every node's agents with composite ids, ordered by node
// id. Agents last seen on a node with no channel, or that failed to answer, are
// included with status "unreachable" until the node answers again or is gone.
// At most one agent is Primary: the first flagged one in node order.
func (m *E2EClient) fanoutPAgents(raw json.RawMessage, out any) error {
	chans := m.channelsSnapshot()
	sort.Slice(chans, func(i, j int) bool { return chans[i].nodeID < chans[j].nodeID })
	type res struct {
		agents  []api.PAgentInfo
		primary string
		ok      bool
		epoch   uint64
	}
	results := make([]res, len(chans))
	var wg sync.WaitGroup
	for i, nc := range chans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.mu.Lock()
			epoch := m.pagentEpoch[nc.nodeID]
			m.mu.Unlock()
			var r api.PAgentListResult
			err := m.callNode(nc.nodeID, api.MethodPAgentList, raw, &r)
			if m.pagentListHook != nil {
				m.pagentListHook(nc.nodeID)
			}
			if err != nil {
				log.Printf("client: warn: pagent.list on node %s failed: %v", nc.nodeID, err)
				return
			}
			agents := make([]api.PAgentInfo, len(r.Agents))
			for j, a := range r.Agents {
				agents[j] = m.pagentOrigin(a, nc.nodeID)
				agents[j].NodeLabel = nc.label
			}
			primary := ""
			if r.Primary != "" {
				primary = session.CompositeID(nc.nodeID, r.Primary)
			}
			results[i] = res{agents: agents, primary: primary, ok: true, epoch: epoch}
		}()
	}
	wg.Wait()

	m.mu.Lock()
	byNode := map[string][]api.PAgentInfo{}
	primaryOf := map[string]string{}
	online := map[string]bool{}
	for i, r := range results {
		nodeID := chans[i].nodeID
		online[nodeID] = true
		// A node lost while its list was in flight bumped the epoch; its
		// reply is stale and must not replace the cache.
		if r.ok && m.pagentEpoch[nodeID] == r.epoch && m.byNode[nodeID] != nil {
			m.pagentCache[nodeID] = r.agents
			byNode[nodeID] = r.agents
			primaryOf[nodeID] = r.primary
			continue
		}
		byNode[nodeID] = unreachableAgents(m.pagentCache[nodeID])
	}
	for nodeID, cached := range m.pagentCache {
		if !online[nodeID] {
			byNode[nodeID] = unreachableAgents(cached)
		}
	}
	m.mu.Unlock()

	nodeIDs := make([]string, 0, len(byNode))
	for id := range byNode {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	merged := []api.PAgentInfo{}
	primary := ""
	for _, id := range nodeIDs {
		for _, a := range byNode[id] {
			if a.Primary {
				if primary == "" {
					primary = a.ID
				} else {
					a.Primary = false
				}
			}
			merged = append(merged, a)
		}
	}
	if primary == "" {
		for _, id := range nodeIDs {
			if primaryOf[id] != "" {
				primary = primaryOf[id]
				break
			}
		}
	}
	return assign(out, api.PAgentListResult{Agents: merged, Primary: primary})
}

func unreachableAgents(in []api.PAgentInfo) []api.PAgentInfo {
	out := make([]api.PAgentInfo, len(in))
	for i, a := range in {
		a.Status = pagentUnreachable
		out[i] = a
	}
	return out
}

func pagentIDEmpty(raw json.RawMessage) bool {
	id, _ := stringField(raw, "agent_id")
	return id == ""
}

// unsetPAgentPrimary clears the primary on every connected node except skip.
func (m *E2EClient) unsetPAgentPrimary(skip string) error {
	var firstErr error
	for _, nc := range m.channelsSnapshot() {
		if nc.nodeID == skip {
			continue
		}
		if err := m.callNode(nc.nodeID, api.MethodPAgentSetPrimary, api.PAgentPrimaryParams{}, nil); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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

// routeByPAgent splits the composite agent_id, routes to its node, and
// composites node-local ids in the result.
func (m *E2EClient) routeByPAgent(method string, raw json.RawMessage, out any) error {
	nodeID, params, err := splitIDField(raw, "agent_id", "agent")
	if err != nil {
		return err
	}
	if method == api.MethodPAgentThreadSend {
		if tid, _ := stringField(raw, "thread_id"); tid != "" {
			tnode, tlocal, ok := session.SplitCompositeID(tid)
			if !ok || tnode != nodeID {
				return &api.RPCError{Code: api.CodeInvalidRequest, Message: "thread " + tid + " does not belong to the agent's node"}
			}
			if params, err = setStringField(params, "thread_id", tlocal); err != nil {
				return err
			}
		}
	}
	switch method {
	case api.MethodPAgentSetPrimary:
		if err := m.callNode(nodeID, method, params, out); err != nil {
			return err
		}
		return m.unsetPAgentPrimary(nodeID)
	case api.MethodPAgentShow, api.MethodPAgentRename:
		var info api.PAgentInfo
		if err := m.callNode(nodeID, method, params, &info); err != nil {
			return err
		}
		return assign(out, m.pagentOrigin(info, nodeID))
	case api.MethodPAgentThreadSend:
		var r api.PAgentSendResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		r.ThreadID = session.CompositeID(nodeID, r.ThreadID)
		if r.SessionID != "" {
			r.SessionID = session.CompositeID(nodeID, r.SessionID)
		}
		return assign(out, r)
	case api.MethodPAgentThreadList:
		var r api.PAgentThreadsResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Threads {
			t := &r.Threads[i]
			t.ID = session.CompositeID(nodeID, t.ID)
			t.AgentID = session.CompositeID(nodeID, t.AgentID)
			if t.SessionID != "" {
				t.SessionID = session.CompositeID(nodeID, t.SessionID)
			}
		}
		return assign(out, r)
	case api.MethodPAgentMemoryLog:
		var r api.PAgentMemoryLogResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Commits {
			if r.Commits[i].ThreadID != "" {
				r.Commits[i].ThreadID = session.CompositeID(nodeID, r.Commits[i].ThreadID)
			}
		}
		return assign(out, r)
	case api.MethodPAgentMemoryProposals:
		var r api.PAgentMemoryProposalsResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Proposals {
			pr := &r.Proposals[i]
			pr.ID = session.CompositeID(nodeID, pr.ID)
			pr.AgentID = session.CompositeID(nodeID, pr.AgentID)
			if pr.ThreadID != "" {
				pr.ThreadID = session.CompositeID(nodeID, pr.ThreadID)
			}
		}
		return assign(out, r)
	}
	return m.callNode(nodeID, method, params, out)
}

// routeByPAgentProposal splits the composite proposal_id and routes to its node.
func (m *E2EClient) routeByPAgentProposal(method string, raw json.RawMessage, out any) error {
	nodeID, params, err := splitIDField(raw, "proposal_id", "proposal")
	if err != nil {
		return err
	}
	return m.callNode(nodeID, method, params, out)
}

// routeByPAgentThread splits the composite thread_id and routes to its node.
func (m *E2EClient) routeByPAgentThread(method string, raw json.RawMessage, out any) error {
	nodeID, params, err := splitIDField(raw, "thread_id", "thread")
	if err != nil {
		return err
	}
	if method != api.MethodPAgentThreadTimeline {
		return m.callNode(nodeID, method, params, out)
	}
	var tl api.PAgentTimeline
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
