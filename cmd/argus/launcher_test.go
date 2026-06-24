package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// drive feeds a key into the model and returns the updated launcherModel.
func drive(m launcherModel, msg tea.Msg) launcherModel {
	mm, _ := m.Update(msg)
	return mm.(launcherModel)
}

func TestMenuStartsOnSpawn(t *testing.T) {
	m := newLauncherModel("")
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (spawn)", m.cursor)
	}
}

func TestMenuNavigationClamps(t *testing.T) {
	m := newLauncherModel("")
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp}) // already at top
	if m.cursor != 0 {
		t.Fatalf("cursor after up at top = %d, want 0", m.cursor)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", m.cursor)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown}) // already at bottom
	if m.cursor != 1 {
		t.Fatalf("cursor after down at bottom = %d, want 1", m.cursor)
	}
}

func TestMenuSpawnChoice(t *testing.T) {
	m := newLauncherModel("")
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // cursor 0 = spawn
	if m.choice.kind != launchSpawn {
		t.Fatalf("choice = %v, want launchSpawn", m.choice.kind)
	}
}

func TestMenuQuitChoice(t *testing.T) {
	m := newLauncherModel("")
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.choice.kind != launchQuit {
		t.Fatalf("choice = %v, want launchQuit", m.choice.kind)
	}
}

func enterGateway(m launcherModel) launcherModel {
	// from menu, move to "Connect to a gateway" and select it
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	return m
}

func TestGatewayEntersInputState(t *testing.T) {
	m := enterGateway(newLauncherModel(""))
	if m.state != stateGateway {
		t.Fatalf("state = %v, want stateGateway", m.state)
	}
	if m.field != 0 {
		t.Fatalf("field = %d, want 0 (url)", m.field)
	}
}

func TestGatewayEscReturnsToMenu(t *testing.T) {
	m := enterGateway(newLauncherModel(""))
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.state != stateMenu {
		t.Fatalf("state = %v, want stateMenu", m.state)
	}
}

func TestGatewayValidSubmit(t *testing.T) {
	m := enterGateway(newLauncherModel(""))
	m.urlIn.SetValue("ws://gw.example.com:8443")
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // url -> token
	if m.field != 1 {
		t.Fatalf("field = %d, want 1 (token)", m.field)
	}
	m.tokenIn.SetValue("secret")
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // submit
	if m.choice.kind != launchGateway {
		t.Fatalf("choice = %v, want launchGateway", m.choice.kind)
	}
	if m.choice.gatewayURL != "ws://gw.example.com:8443" {
		t.Fatalf("gatewayURL = %q", m.choice.gatewayURL)
	}
	if m.choice.token != "secret" {
		t.Fatalf("token = %q", m.choice.token)
	}
}

func TestGatewayInvalidURLStays(t *testing.T) {
	m := enterGateway(newLauncherModel(""))
	m.urlIn.SetValue("http://nope")                   // unsupported scheme
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // url -> token
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // submit
	if m.choice.kind == launchGateway {
		t.Fatal("invalid URL should not produce a launchGateway choice")
	}
	if m.state != stateGateway {
		t.Fatalf("state = %v, want stateGateway (stay on form)", m.state)
	}
	if m.errMsg == "" {
		t.Fatal("expected an inline error message")
	}
}

func TestTokenPrefilled(t *testing.T) {
	m := newLauncherModel("sekret")
	if m.tokenIn.Value() != "sekret" {
		t.Fatalf("tokenIn = %q, want sekret", m.tokenIn.Value())
	}
}

func TestGatewaySubmitUsesPrefilledToken(t *testing.T) {
	m := enterGateway(newLauncherModel("sekret"))
	m.urlIn.SetValue("ws://gw.example.com:8443")
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // url -> token
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // submit
	if m.choice.kind != launchGateway {
		t.Fatalf("choice = %v, want launchGateway", m.choice.kind)
	}
	if m.choice.token != "sekret" {
		t.Fatalf("token = %q, want sekret", m.choice.token)
	}
}

func TestWindowSizeSetsInputWidth(t *testing.T) {
	m := newLauncherModel("")
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = mm.(launcherModel)
	if m.urlIn.Width() <= 0 {
		t.Fatalf("urlIn width = %d, want > 0", m.urlIn.Width())
	}
}
