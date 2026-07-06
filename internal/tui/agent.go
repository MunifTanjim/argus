package tui

import "image/color"

// agentLabel returns a display label and accent color for a session's agent.
// Unknown agents fall back to the raw id in a neutral color.
func agentLabel(agent string) (string, color.Color) {
	switch agent {
	case "":
		return "", nil
	case "claude":
		return "Claude", ColorAgentClaude
	case "codex":
		return "Codex", ColorAgentCodex
	case "antigravity":
		return "Antigravity", ColorAgentAntigravity
	default:
		return agent, ColorTextDim
	}
}

func agentLabelColor(accent color.Color, selected bool) color.Color {
	if selected {
		return accent
	}
	return ColorTextDim
}
