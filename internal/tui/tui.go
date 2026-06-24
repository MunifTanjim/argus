// Package tui implements argus's terminal client: a Bubble Tea dashboard that
// connects to argusd, lists sessions, reflects live registry events, shows a
// session's transcript, and provides a live screen passthrough for direct pane
// interaction.
package tui

import (
	"os"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// Client is the connection the TUI drives. Both *api.Client and
// *api.ReconnectingClient satisfy it; the latter recovers from dropped connections,
// surfacing transitions on States() and re-dialing on Reconnect().
type Client interface {
	Call(method string, params, out any) error
	Events() <-chan api.Notification
	States() <-chan bool
	Reconnect()
	Close() error
}

// Run connects the TUI to the node and blocks until the user quits.
func Run(client Client) error {
	// Detect the terminal background ONCE, before Bubble Tea enters alt-screen.
	// lipgloss queries it via OSC 11, which can fail once alt-screen is active.
	hasDark := lipgloss.HasDarkBackground(os.Stdin, os.Stderr)
	initTheme(hasDark)
	initIcons()
	initStyles()

	keyCh := make(chan paneKey, 512)
	go sendKeyLoop(client, keyCh)

	p := tea.NewProgram(newModel(client, hasDark, keyCh))
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
	_, err := p.Run()
	return err
}

// paneKey is one keystroke bound for a session's pane: either literal text (sent
// with -l) or a named tmux key (Enter, Escape, BSpace, C-c, Up, ...).
type paneKey struct {
	id      string
	literal string
	named   string
}

// sendKeyLoop drains keystrokes in arrival order and forwards them to the pane,
// merging consecutive literals into one send-keys to limit tmux spawns (and make
// paste cheap). A single goroutine guarantees the keys land in order.
func sendKeyLoop(client Client, keyCh chan paneKey) {
	for k := range keyCh {
		if k.named != "" {
			_ = client.Call(api.MethodSessionKey, api.KeyParams{SessionID: k.id, Keys: []string{k.named}}, nil)
			continue
		}
		text := k.literal
		for draining := true; draining; {
			select {
			case n := <-keyCh:
				if n.named != "" {
					if text != "" {
						_ = client.Call(api.MethodSessionInput, api.InputParams{SessionID: k.id, Text: text}, nil)
					}
					_ = client.Call(api.MethodSessionKey, api.KeyParams{SessionID: n.id, Keys: []string{n.named}}, nil)
					text, draining = "", false
				} else {
					text += n.literal
				}
			default:
				draining = false
			}
		}
		if text != "" {
			_ = client.Call(api.MethodSessionInput, api.InputParams{SessionID: k.id, Text: text}, nil)
		}
	}
}
