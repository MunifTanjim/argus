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

	keyCh := make(chan paneKey, 512)
	go sendKeyLoop(client, keyCh)

	p := tea.NewProgram(newModel(client, hasDark, keyCh, logs))
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

// paneKey is one keystroke for a session's pane: literal text or a named tmux key.
type paneKey struct {
	id      string
	literal string
	named   string
}

// sendKeyLoop forwards keystrokes to the pane in arrival order, coalescing
// consecutive literals into one send-keys to limit tmux spawns (cheap paste).
// A single goroutine guarantees ordering.
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
