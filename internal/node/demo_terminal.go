package node

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// demoTerminalChunk is the byte window per emitted frame; small enough to look
// like live output when replayed.
const demoTerminalChunk = 256

// demoTerminalOpen replays a session's canned terminal bytes as terminal.output
// notifications, then holds without an exit so the screen stays populated for a
// screenshot. Unknown sessions emit nothing.
func (d *Node) demoTerminalOpen(ctx context.Context, p api.TerminalOpenParams) (any, error) {
	n, ok := api.NotifierFrom(ctx)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no connection notifier"}
	}
	data := d.demoTerminals[p.SessionID]
	go func() {
		for off := 0; off < len(data); off += demoTerminalChunk {
			end := off + demoTerminalChunk
			if end > len(data) {
				end = len(data)
			}
			chunk := base64.StdEncoding.EncodeToString(data[off:end])
			if err := n.Notify(api.MethodTerminalOutput, api.TerminalOutput{TermID: p.TermID, Data: chunk}); err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(40 * time.Millisecond):
			}
		}
		// Hold: no terminal.exited, so the viewer keeps the final frame.
	}()
	return nil, nil
}
