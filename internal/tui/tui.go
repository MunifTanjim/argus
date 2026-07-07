// Package tui is argus's Bubble Tea terminal client: session list, live registry
// events, transcript view, and screen passthrough for direct pane interaction.
package tui

import (
	"os"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/logbuf"
)

// Client is the connection the TUI drives. *api.ReconnectingClient also satisfies
// it, surfacing connection transitions on States() and re-dialing on Reconnect().
type Client interface {
	Call(method string, params, out any) error
	Events() <-chan api.Notification
	States() <-chan bool
	Reconnect()
	Close() error
}

// Run connects the TUI and blocks until the user quits. Non-nil logs (embedded
// node) are tailed in the Logs tab.
func Run(client Client, logs *logbuf.Buffer) error {
	// Detect background ONCE: the OSC 11 query can fail once alt-screen is active.
	hasDark := lipgloss.HasDarkBackground(os.Stdin, os.Stderr)
	initTheme(hasDark)
	initIcons()
	initStyles()

	m := newModel(client, hasDark, logs)
	go sendTermKeyLoop(client, m.termKeyCh) // single ordered sender for live-terminal input
	p := tea.NewProgram(m)
	go func() {
		events, states := client.Events(), client.States()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return // plain (non-reconnecting) client: connection ended
				}
				p.Send(notificationMsg(ev))
			case connected, ok := <-states:
				if !ok {
					return
				}
				p.Send(connStateMsg{connected: connected})
			}
		}
	}()
	if logs != nil {
		go func() {
			for range logs.Notify() {
				p.Send(logTickMsg{})
			}
		}()
	}
	_, err := p.Run()
	return err
}
