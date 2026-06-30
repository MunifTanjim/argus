package main

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

// newUnpairCmd builds `argus unpair [token]`: revoke a paired client token. With an
// argument, removes it non-interactively; otherwise lists tokens and shows a picker.
func newUnpairCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "unpair [token]",
		Short:         "Revoke a paired client device",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			if cfg.Gateway.URL == "" {
				return fail(cmd, fmt.Errorf("--gateway ws(s)://host is required (unpairing talks to the gateway over the wire)"))
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := dialGatewayClient(ctx, cfg.Gateway.URL, cfg.Token)
			if err != nil {
				return fail(cmd, fmt.Errorf("connect: %w", err))
			}
			defer client.Close()

			if len(args) == 1 {
				return removeToken(cmd, client, args[0])
			}

			var list []api.ClientTokenInfo
			if err := client.Call(api.MethodClientsList, nil, &list); err != nil {
				return fail(cmd, fmt.Errorf("list clients: %w", err))
			}
			if len(list) == 0 {
				shell.StdOutF("no paired clients\n")
				return nil
			}
			tok, err := pickToken(list)
			if err != nil {
				return fail(cmd, err)
			}
			if tok == "" {
				return nil // cancelled
			}
			return removeToken(cmd, client, tok)
		},
	}
	addGatewayClientFlags(cmd.Flags())
	return cmd
}

// removeToken revokes one client token on the gateway.
func removeToken(cmd *cobra.Command, client *api.Client, token string) error {
	if err := client.Call(api.MethodClientsRemove, api.ClientRemoveParams{Token: token}, nil); err != nil {
		return fail(cmd, fmt.Errorf("remove %s: %w", shortToken(token), err))
	}
	shell.StdOutF("revoked %s\n", shortToken(token))
	return nil
}

// shortToken abbreviates a token for display (full tokens are long hex strings).
func shortToken(tok string) string {
	if len(tok) <= 12 {
		return tok
	}
	return tok[:12] + "…"
}

// pickToken runs an interactive picker over the client tokens and returns the
// chosen token, or "" if the user cancelled.
func pickToken(items []api.ClientTokenInfo) (string, error) {
	m, err := tea.NewProgram(pickerModel{items: items, chosen: -1}).Run()
	if err != nil {
		return "", err
	}
	pm := m.(pickerModel)
	if pm.chosen < 0 || pm.chosen >= len(items) {
		return "", nil
	}
	return items[pm.chosen].Token, nil
}

// pickerModel is a minimal single-select list over client tokens.
type pickerModel struct {
	items  []api.ClientTokenInfo
	cursor int
	chosen int // -1 until an item is selected
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		m.chosen = m.cursor
		return m, tea.Quit
	case "q", "esc", "ctrl+c":
		m.chosen = -1
		return m, tea.Quit
	}
	return m, nil
}

func (m pickerModel) View() tea.View {
	var b strings.Builder
	b.WriteString("Select a client to revoke (↑/↓, enter to revoke, q to cancel):\n\n")
	for i, it := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "▸ "
		}
		created := it.CreatedAt
		if created == "" {
			created = "unknown"
		}
		b.WriteString(fmt.Sprintf("%s%s  paired %s\n", cursor, shortToken(it.Token), created))
	}
	return tea.NewView(b.String())
}
