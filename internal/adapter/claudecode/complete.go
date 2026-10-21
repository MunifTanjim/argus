package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	return items, nil
}
