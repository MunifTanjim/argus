package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// pagentsMsg is one load of the persistent agents: the list, the primary
// agent's id, and each reachable agent's threads.
type pagentsMsg struct {
	names   map[string]string
	agents  []api.PAgentInfo
	primary string
	threads map[string][]api.PAgentThreadInfo
	err     error
}

// loadPAgentsCmd fetches the agent list and each reachable agent's threads.
// Nodes without pagent.list report no agents.
func (m model) loadPAgentsCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.PAgentListResult
		err := client.Call(api.MethodPAgentList, api.PAgentListParams{}, &res)
		if api.IsMethodNotFound(err) {
			return pagentsMsg{names: map[string]string{}, threads: map[string][]api.PAgentThreadInfo{}}
		}
		names := make(map[string]string, len(res.Agents))
		threads := map[string][]api.PAgentThreadInfo{}
		for _, a := range res.Agents {
			names[a.ID] = a.Name
			if err != nil || a.Status == pagentUnreachable {
				continue
			}
			var tr api.PAgentThreadsResult
			if client.Call(api.MethodPAgentThreadList, api.PAgentRef{AgentID: a.ID}, &tr) == nil {
				threads[a.ID] = tr.Threads
			}
		}
		return pagentsMsg{names: names, agents: res.Agents, primary: res.Primary, threads: threads, err: err}
	}
}

const pagentUnreachable = "unreachable"

func (m model) pagent(id string) (api.PAgentInfo, bool) {
	for _, a := range m.pagents {
		if a.ID == id {
			return a, true
		}
	}
	return api.PAgentInfo{}, false
}

func (m model) pagentPrimaryOf(a api.PAgentInfo) bool {
	return a.Primary || (a.ID != "" && a.ID == m.pagentPrimary)
}

// pagentUnread reports whether any of the agent's threads has unread activity.
func (m model) pagentUnread(id string) bool {
	for _, t := range m.pagentThreads[id] {
		if t.Unread {
			return true
		}
	}
	return false
}
