package client

import (
	"encoding/json"
	"log"
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

// fanoutPAgents merges every node's agents with composite ids. Agents last seen
// on a node that is offline (within the grace window) or failed to answer are
// included with status "unreachable". The merged Primary is the first non-empty
// one in node order.
func (m *E2EClient) fanoutPAgents(raw json.RawMessage, out any) error {
	chans := m.channelsSnapshot()
	type res struct {
		agents  []api.PAgentInfo
		primary string
		ok      bool
	}
	results := make([]res, len(chans))
	var wg sync.WaitGroup
	for i, nc := range chans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r api.PAgentListResult
			if err := m.callNode(nc.nodeID, api.MethodPAgentList, raw, &r); err != nil {
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
			results[i] = res{agents: agents, primary: primary, ok: true}
		}()
	}
	wg.Wait()

	merged := []api.PAgentInfo{}
	primary := ""
	m.mu.Lock()
	seen := map[string]bool{}
	for i, r := range results {
		nodeID := chans[i].nodeID
		seen[nodeID] = true
		if r.ok {
			m.pagentCache[nodeID] = r.agents
			delete(m.pagentLostAt, nodeID)
			merged = append(merged, r.agents...)
			if primary == "" {
				primary = r.primary
			}
			continue
		}
		merged = append(merged, unreachableAgents(m.pagentCache[nodeID])...)
	}
	for nodeID, cached := range m.pagentCache {
		if seen[nodeID] {
			continue
		}
		if lost, ok := m.pagentLostAt[nodeID]; !ok || time.Since(lost) > m.offlineGrace {
			delete(m.pagentCache, nodeID)
			delete(m.pagentLostAt, nodeID)
			continue
		}
		merged = append(merged, unreachableAgents(cached)...)
	}
	m.mu.Unlock()
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

// clearPAgentPrimary unsets the primary on every node.
func (m *E2EClient) clearPAgentPrimary(raw json.RawMessage) error {
	var firstErr error
	for _, nc := range m.channelsSnapshot() {
		if err := m.callNode(nc.nodeID, api.MethodPAgentSetPrimary, raw, nil); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func splitIDField(raw json.RawMessage, field, what string) (nodeID, local string, params json.RawMessage, err error) {
	composite, err := stringField(raw, field)
	if err != nil {
		return "", "", nil, err
	}
	nodeID, local, ok := session.SplitCompositeID(composite)
	if !ok {
		return "", "", nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: what + " id is not gateway-qualified: " + composite}
	}
	params, err = setStringField(raw, field, local)
	return nodeID, local, params, err
}

// routeByPAgent splits the composite agent_id, routes to its node, and
// composites node-local ids in the result.
func (m *E2EClient) routeByPAgent(method string, raw json.RawMessage, out any) error {
	nodeID, _, params, err := splitIDField(raw, "agent_id", "agent")
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
	}
	return m.callNode(nodeID, method, params, out)
}

// routeByPAgentThread splits the composite thread_id and routes to its node.
func (m *E2EClient) routeByPAgentThread(method string, raw json.RawMessage, out any) error {
	nodeID, _, params, err := splitIDField(raw, "thread_id", "thread")
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
