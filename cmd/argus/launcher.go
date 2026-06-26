package main

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

// launchKind is which path the user chose at the launcher screen.
type launchKind int

const (
	launchQuit           launchKind = iota // user backed out; spawn nothing, exit cleanly
	launchSpawnIsolated                    // start an ephemeral isolated node
	launchSpawnConnected                   // start an ephemeral connected node
	launchGateway                          // connect to the entered gateway URL + token
)

// launchChoice is the launcher's result. gatewayURL/token are set for launchGateway
// and launchSpawnConnected (both dial a gateway).
type launchChoice struct {
	kind       launchKind
	gatewayURL string
	token      string
}

// launcherState is the launcher's current screen: the two-item menu, or the
// gateway URL/token entry form.
type launcherState int

const (
	stateMenu launcherState = iota
	stateGateway
)

const (
	menuSpawnIsolated = iota
	menuSpawnConnected
	menuGateway
)

// launcherModel is the pre-TUI connect chooser. It is shown only in local mode
// when no node is listening; its choice drives how cmd/argus builds the client.
type launcherModel struct {
	state          launcherState
	cursor         int  // menu cursor: menuSpawnIsolated, menuSpawnConnected, or menuGateway
	spawnConnected bool // gateway form is for a connected spawn, not a plain gateway connect
	field          int  // gateway form focus: 0 = url, 1 = token
	urlIn          textinput.Model
	tokenIn        textinput.Model
	errMsg         string
	choice         launchChoice
}

func newLauncherModel(token string) launcherModel {
	url := textinput.New()
	url.Placeholder = "ws://host:8443  |  ssh://user@host"
	url.SetWidth(48)
	tok := textinput.New()
	tok.Placeholder = "token"
	tok.EchoMode = textinput.EchoPassword
	tok.SetWidth(48)
	tok.SetValue(token)
	return launcherModel{state: stateMenu, urlIn: url, tokenIn: tok}
}

func (m launcherModel) Init() tea.Cmd { return nil }

func (m launcherModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		w := ws.Width - 10
		if w < 20 {
			w = 20
		}
		m.urlIn.SetWidth(w)
		m.tokenIn.SetWidth(w)
		return m, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.state == stateMenu {
		return m.updateMenu(key)
	}
	return m.updateGateway(key)
}

func (m launcherModel) updateMenu(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < menuGateway {
			m.cursor++
		}
	case "enter":
		if m.cursor == menuSpawnIsolated {
			m.choice = launchChoice{kind: launchSpawnIsolated}
			return m, tea.Quit
		}
		// menuSpawnConnected and menuGateway both need a gateway URL + token, so
		// they go through the form; spawnConnected picks the kind on submit.
		m.spawnConnected = m.cursor == menuSpawnConnected
		m.state = stateGateway
		m.field = 0
		return m, m.urlIn.Focus()
	case "q", "esc", "ctrl+c":
		m.choice = launchChoice{kind: launchQuit}
		return m, tea.Quit
	}
	return m, nil
}

func (m launcherModel) updateGateway(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.state = stateMenu
		m.errMsg = ""
		m.urlIn.Blur()
		m.tokenIn.Blur()
		return m, nil
	case "ctrl+c":
		m.choice = launchChoice{kind: launchQuit}
		return m, tea.Quit
	case "tab", "shift+tab":
		return m.toggleField()
	case "enter":
		if m.field == 0 {
			m.field = 1
			m.urlIn.Blur()
			return m, m.tokenIn.Focus()
		}
		return m.submitGateway()
	}

	// Forward typing to the focused field.
	var cmd tea.Cmd
	if m.field == 0 {
		m.urlIn, cmd = m.urlIn.Update(key)
	} else {
		m.tokenIn, cmd = m.tokenIn.Update(key)
	}
	return m, cmd
}

func (m launcherModel) toggleField() (tea.Model, tea.Cmd) {
	if m.field == 0 {
		m.field = 1
		m.urlIn.Blur()
		return m, m.tokenIn.Focus()
	}
	m.field = 0
	m.tokenIn.Blur()
	return m, m.urlIn.Focus()
}

func (m launcherModel) submitGateway() (tea.Model, tea.Cmd) {
	url := m.urlIn.Value()
	if _, _, err := resolveGatewayURL(url, routeClient, nil); err != nil {
		m.errMsg = err.Error()
		return m, nil
	}
	kind := launchGateway
	if m.spawnConnected {
		kind = launchSpawnConnected
	}
	m.choice = launchChoice{kind: kind, gatewayURL: url, token: m.tokenIn.Value()}
	return m, tea.Quit
}

func (m launcherModel) View() tea.View {
	var b string
	title := lipgloss.NewStyle().Bold(true).Render("Argus — no node running")
	if m.state == stateMenu {
		items := []string{"Spawn isolated node (ephemeral)", "Spawn gateway connected node (ephemeral)", "Connect to gateway"}
		body := ""
		for i, it := range items {
			prefix := "  "
			if i == m.cursor {
				prefix = "❯ "
			}
			body += prefix + it + "\n"
		}
		hint := lipgloss.NewStyle().Faint(true).Render("↑/↓ · enter · q to quit")
		b = title + "\n\n" + body + "\n" + hint
	} else {
		b = m.gatewayView()
	}
	return tea.NewView(b)
}

func (m launcherModel) gatewayView() string {
	heading := "Connect to gateway"
	if m.spawnConnected {
		heading = "Spawn gateway connected node"
	}
	title := lipgloss.NewStyle().Bold(true).Render(heading)
	urlLabel, tokLabel := "  URL:   ", "  Token: "
	if m.field == 0 {
		urlLabel = "❯ URL:   "
	} else {
		tokLabel = "❯ Token: "
	}
	body := urlLabel + m.urlIn.View() + "\n" + tokLabel + m.tokenIn.View()
	hint := lipgloss.NewStyle().Faint(true).Render("tab to switch · enter to submit · esc back")
	out := title + "\n\n" + body + "\n"
	if m.errMsg != "" {
		out += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(m.errMsg) + "\n"
	}
	return out + "\n" + hint
}

// runLauncher runs the chooser and returns the user's choice.
func runLauncher(token string) (launchChoice, error) {
	fm, err := tea.NewProgram(newLauncherModel(token)).Run()
	if err != nil {
		return launchChoice{}, err
	}
	return fm.(launcherModel).choice, nil
}
