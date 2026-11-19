package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

var _ adapter.PromptCompleter = (*piAdapter)(nil)

var newCompletion = api.PromptCompletion{Name: "new", Description: "start a new session"}

// commandsTimeout bounds the wait for pi's command list. A gate loaded before
// Argus asked for one never answers.
var commandsTimeout = 2 * time.Second

// gateCommand is the gate's diagnostic command; it reports to pi's own UI.
const gateCommand = "argus-ping"

// piCommand is one entry of pi's get_commands: an extension command, a prompt
// template, or a skill ("skill:name").
type piCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (a *piAdapter) CompletePrompt(ctx context.Context, sess session.Session, trigger string) ([]api.PromptCompletion, error) {
	if trigger != "/" {
		return nil, nil
	}
	d, err := a.live()
	if err != nil {
		return nil, err
	}
	return d.completions(ctx, sess.AgentSessionID)
}

func (d *discoverer) completions(ctx context.Context, id string) ([]api.PromptCompletion, error) {
	if p := d.get(id); p != nil {
		return withBuiltins(true, d.ownedCommands(ctx, p)), nil
	}
	return nil, fmt.Errorf("pi: unknown session %s", id)
}

// ownedCommands gives nil when pi does not answer, so the menu still offers
// the built-ins.
func (d *discoverer) ownedCommands(ctx context.Context, p *sessionProc) []piCommand {
	ctx, cancel := context.WithTimeout(ctx, commandsTimeout)
	defer cancel()
	data, err := p.conn.call(ctx, d.nextID(), "get_commands", nil)
	if err != nil || len(data) == 0 {
		return nil
	}
	var res struct {
		Commands []piCommand `json:"commands"`
	}
	if json.Unmarshal(data, &res) != nil {
		return nil
	}
	return res.Commands
}

// withBuiltins puts the commands Argus runs itself ahead of pi's; a pi command
// under a built-in's name would never run.
func withBuiltins(includeNew bool, cmds []piCommand) []api.PromptCompletion {
	items := []api.PromptCompletion{adapter.CompactCompletion}
	if includeNew {
		items = append(items, newCompletion)
	}
	for _, c := range cmds {
		if c.Name == adapter.CompactCompletion.Name || c.Name == newCompletion.Name || c.Name == gateCommand {
			continue
		}
		items = append(items, api.PromptCompletion{Name: c.Name, Description: c.Description})
	}
	return items
}
