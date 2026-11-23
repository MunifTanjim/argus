package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// botsMsg is one load of the bots: the list, the primary
// bot's id, and each reachable bot's threads.
type botsMsg struct {
	names   map[string]string
	bots    []api.BotInfo
	primary string
	threads map[string][]api.BotThreadInfo
	err     error
}

// loadBotsCmd fetches the bot list and each reachable bot's threads.
// Nodes without bot.list report no bots.
func (m model) loadBotsCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.BotListResult
		err := client.Call(api.MethodBotList, api.BotListParams{}, &res)
		if api.IsMethodNotFound(err) {
			return botsMsg{names: map[string]string{}, threads: map[string][]api.BotThreadInfo{}}
		}
		names := make(map[string]string, len(res.Bots))
		threads := map[string][]api.BotThreadInfo{}
		for _, a := range res.Bots {
			names[a.ID] = a.Name
			if err != nil || a.Status == botUnreachable {
				continue
			}
			var tr api.BotThreadsResult
			if client.Call(api.MethodBotThreadList, api.BotRef{BotID: a.ID}, &tr) == nil {
				threads[a.ID] = tr.Threads
			}
		}
		return botsMsg{names: names, bots: res.Bots, primary: res.Primary, threads: threads, err: err}
	}
}

const botUnreachable = "unreachable"

func (m model) bot(id string) (api.BotInfo, bool) {
	for _, a := range m.bots {
		if a.ID == id {
			return a, true
		}
	}
	return api.BotInfo{}, false
}

func (m model) botPrimaryOf(a api.BotInfo) bool {
	return a.Primary || (a.ID != "" && a.ID == m.botPrimary)
}

// botUnread reports whether any of the bot's threads has unread activity.
func (m model) botUnread(id string) bool {
	for _, t := range m.botThreads[id] {
		if t.Unread {
			return true
		}
	}
	return false
}
