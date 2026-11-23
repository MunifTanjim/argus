package client

import (
	"encoding/json"
	"log"
	"sort"
	"sync"
	"time"

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
// At most one agent is Primary: the one set most recently. Nodes that answered
// with an older primary have it cleared, so the fleet keeps one.
func (m *E2EClient) fanoutPAgents(raw json.RawMessage, out any) error {
	chans := m.channelsSnapshot()
	sort.Slice(chans, func(i, j int) bool { return chans[i].nodeID < chans[j].nodeID })
	type res struct {
		agents  []api.PAgentInfo
		primary string
		since   time.Time
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
			results[i] = res{agents: agents, primary: primary, since: r.PrimarySince, ok: true, epoch: epoch}
		}()
	}
	wg.Wait()

	m.mu.Lock()
	byNode := map[string][]api.PAgentInfo{}
	primaryOf := map[string]string{}
	answered := map[string]bool{}
	online := map[string]bool{}
	for i, r := range results {
		nodeID := chans[i].nodeID
		online[nodeID] = true
		// A node lost while its list was in flight bumped the epoch; its
		// reply is stale and must not replace the cache.
		if r.ok && m.pagentEpoch[nodeID] == r.epoch && m.byNode[nodeID] != nil {
			m.pagentCache[nodeID] = r.agents
			m.pagentPrimary[nodeID] = pagentPrimary{id: r.primary, since: r.since}
			byNode[nodeID] = r.agents
			answered[nodeID] = true
			continue
		}
		byNode[nodeID] = unreachableAgents(m.pagentCache[nodeID])
	}
	for nodeID, cached := range m.pagentCache {
		if !online[nodeID] {
			byNode[nodeID] = unreachableAgents(cached)
		}
	}
	for nodeID := range byNode {
		if p := m.pagentPrimary[nodeID]; p.id != "" {
			primaryOf[nodeID] = p.id
		}
	}
	since := map[string]time.Time{}
	for nodeID := range primaryOf {
		since[nodeID] = m.pagentPrimary[nodeID].since
	}
	m.mu.Unlock()

	nodeIDs := make([]string, 0, len(byNode))
	for id := range byNode {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	// The newest primary wins; on a tie, the first in node order.
	primary, primaryNode := "", ""
	for _, id := range nodeIDs {
		if p := primaryOf[id]; p != "" && (primary == "" || since[id].After(since[primaryNode])) {
			primary, primaryNode = p, id
		}
	}
	merged := []api.PAgentInfo{}
	for _, id := range nodeIDs {
		for _, a := range byNode[id] {
			a.Primary = primary != "" && a.ID == primary
			merged = append(merged, a)
		}
	}
	for _, id := range nodeIDs {
		if id != primaryNode && primaryOf[id] != "" && answered[id] {
			go m.clearStalePrimary(id)
		}
	}
	return assign(out, api.PAgentListResult{Agents: merged, Primary: primary})
}

// pagentPrimary is a node's primary agent (composite id) and when it was set.
type pagentPrimary struct {
	id    string
	since time.Time
}

// clearStalePrimary unsets the primary of nodeID, which another node's newer
// primary replaced while nodeID was away.
func (m *E2EClient) clearStalePrimary(nodeID string) {
	if err := m.callNode(nodeID, api.MethodPAgentSetPrimary, api.PAgentPrimaryParams{}, nil); err != nil {
		log.Printf("client: warn: clear old primary on node %s: %v", nodeID, err)
	}
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
// A node it can't reach keeps its primary until a later agent list finds the
// newer one and clears it.
func (m *E2EClient) unsetPAgentPrimary(skip string) {
	for _, nc := range m.channelsSnapshot() {
		if nc.nodeID == skip {
			continue
		}
		if err := m.callNode(nc.nodeID, api.MethodPAgentSetPrimary, api.PAgentPrimaryParams{}, nil); err != nil {
			log.Printf("client: warn: unset primary on node %s: %v", nc.nodeID, err)
		}
	}
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
	if method == api.MethodPAgentThreadSend || method == api.MethodPAgentMemoryUndo {
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
		m.unsetPAgentPrimary(nodeID)
		return nil
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
	case api.MethodPAgentActivity:
		var r api.PAgentActivityResult
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
	case api.MethodPAgentRoutineList:
		var r api.PAgentRoutineListResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		for i := range r.Routines {
			r.Routines[i].AgentID = session.CompositeID(nodeID, r.Routines[i].AgentID)
		}
		return assign(out, r)
	case api.MethodPAgentUsage:
		var r api.PAgentUsageResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		r.AgentID = session.CompositeID(nodeID, r.AgentID)
		return assign(out, r)
	case api.MethodPAgentRoutinePause, api.MethodPAgentRoutineResume:
		var r api.PAgentRoutineInfo
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		r.AgentID = session.CompositeID(nodeID, r.AgentID)
		return assign(out, r)
	case api.MethodPAgentRoutineRuns:
		var r api.PAgentRoutineRunsResult
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
	if method == api.MethodPAgentOfferStart {
		var r api.PAgentSendResult
		if err := m.callNode(nodeID, method, params, &r); err != nil {
			return err
		}
		r.ThreadID = session.CompositeID(nodeID, r.ThreadID)
		if r.SessionID != "" {
			r.SessionID = session.CompositeID(nodeID, r.SessionID)
		}
		return assign(out, r)
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
