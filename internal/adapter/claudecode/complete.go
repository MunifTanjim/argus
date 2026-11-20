package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/session"
)

var _ adapter.PromptCompleter = (*ccAdapter)(nil)

// CompletePrompt lists Claude's slash commands, skills included, as its mod
// reports them. A session whose mod cannot list them completes to nothing.
func (a *ccAdapter) CompletePrompt(ctx context.Context, sess session.Session, trigger string) ([]api.PromptCompletion, error) {
	if trigger != "/" || a.bridge == nil || sess.AgentSessionID == "" {
		return nil, nil
	}
	data, err := a.bridge.Request(ctx, sess.AgentSessionID, modbridge.Command{Kind: modbridge.KindCommands})
	if errors.Is(err, modbridge.ErrUnreachable) || errors.Is(err, modbridge.ErrUnsupported) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []api.PromptCompletion
	if len(data) > 0 {
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, fmt.Errorf("claude command list: %w", err)
		}
	}
	return slices.DeleteFunc(items, func(c api.PromptCompletion) bool { return hiddenCommands[c.Name] }), nil
}

// hiddenCommands are built-ins that open a panel, picker or dialog in Claude's
// terminal, change its display, or only run in server-launched sessions.
var hiddenCommands = map[string]bool{
	"__remote-workflow":    true,
	"agents":               true,
	"background":           true,
	"btw":                  true,
	"bug":                  true,
	"color":                true,
	"config":               true,
	"context":              true,
	"copy":                 true,
	"diff":                 true,
	"export":               true,
	"feedback":             true,
	"focus":                true,
	"heapdump":             true,
	"help":                 true,
	"hooks":                true,
	"ide":                  true,
	"keybindings":          true,
	"list-agents":          true,
	"login":                true,
	"mcp":                  true,
	"memory":               true,
	"mobile":               true,
	"passes":               true,
	"permissions":          true,
	"plugin":               true,
	"powerup":              true,
	"pro-trial-expired":    true,
	"radio":                true,
	"release-notes":        true,
	"resume":               true,
	"rewind":               true,
	"sandbox":              true,
	"scroll-speed":         true,
	"setup-bedrock":        true,
	"setup-vertex":         true,
	"skills":               true,
	"status":               true,
	"stickers":             true,
	"tasks":                true,
	"terminal-setup":       true,
	"theme":                true,
	"tui":                  true,
	"usage":                true,
	"workflow-launch-exec": true,
	"workflows":            true,
}
