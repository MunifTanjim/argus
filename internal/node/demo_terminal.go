package node

import (
	"context"
	"encoding/base64"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MunifTanjim/argus/internal/api"
)

// demoTerminalChunk is the byte window per emitted frame; small enough to look
// like live output when replayed.
const demoTerminalChunk = 256

var ansiCSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// terminalWidth is the visible width of the longest line in canned terminal
// bytes.
func terminalWidth(b []byte) int {
	w := 0
	for _, line := range strings.Split(ansiCSI.ReplaceAllString(string(b), ""), "\n") {
		w = max(w, utf8.RuneCountInString(strings.TrimRight(line, "\r")))
	}
	return w
}

// demoTerminalOpen replays canned terminal bytes, of a node terminal or else of
// a session, as terminal.output notifications, then holds without an exit so the
// screen stays populated for a screenshot. Unknown ids emit nothing.
func (d *Node) demoTerminalOpen(ctx context.Context, p api.TerminalOpenParams) (any, error) {
	n, ok := api.NotifierFrom(ctx)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no connection notifier"}
	}
	data := d.demoSessionTerminals[p.SessionID]
	if wide, ok := d.demoWideTerminals[p.SessionID]; ok && p.Cols >= terminalWidth(wide) {
		data = wide
	}
	if p.TerminalID != "" {
		data = d.demoNodeTerminals[p.TerminalID]
	}
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
