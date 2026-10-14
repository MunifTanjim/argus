package claudecode

import (
	"context"
	"errors"
	"fmt"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/session"
)

var _ adapter.Renamer = (*ccAdapter)(nil)

// RenameSession runs /rename through the mod. Without a mod connection it fails
// rather than write disk state a running process would never adopt. Behind a
// running turn the mod only accepts it, so it is not applied on return; the mod
// reports the rename once it ran.
func (a *ccAdapter) RenameSession(ctx context.Context, sess session.Session, name string) (bool, error) {
	if sess.AgentSessionID == "" {
		return false, fmt.Errorf("claude: session has no agent session id")
	}
	ran, err := a.sendMod(ctx, sess, modbridge.Command{Kind: modbridge.KindPrompt, Text: "/rename " + name})
	if errors.Is(err, api.ErrNoTerminalControl) {
		return false, fmt.Errorf("rename needs the Argus plugin connection: %w", api.ErrNoTerminalControl)
	}
	return ran, err
}
