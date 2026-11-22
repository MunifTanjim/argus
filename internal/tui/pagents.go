package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// pagentsMsg is one load of the persistent agent names.
type pagentsMsg struct {
	names map[string]string
	err   error
}

// loadPAgentsCmd fetches the agent list. Nodes without pagent.list report no
// agents.
func (m model) loadPAgentsCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.PAgentListResult
		err := client.Call(api.MethodPAgentList, api.PAgentListParams{}, &res)
		if api.IsMethodNotFound(err) {
			return pagentsMsg{names: map[string]string{}}
		}
		names := make(map[string]string, len(res.Agents))
		for _, a := range res.Agents {
			names[a.ID] = a.Name
		}
		return pagentsMsg{names: names, err: err}
	}
}
