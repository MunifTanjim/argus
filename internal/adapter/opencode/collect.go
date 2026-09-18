package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// collectSessionFiles fetches a session's messages from the live service and
// stages them as a single JSON file for the export bundle. sessionID is the
// OpenCode session id (the adapter's transcript path). The returned file lives
// under the OS temp dir; the export handler removes it after archiving.
func collectSessionFiles(sessionID string) ([]adapter.BundledFile, error) {
	c, ok := serviceClient()
	if !ok {
		return nil, fmt.Errorf("opencode: service unavailable; cannot export session %s", sessionID)
	}
	msgs, err := c.readMessagesRaw(context.Background(), sessionID)
	if err != nil {
		return nil, fmt.Errorf("opencode: read messages for %s: %w", sessionID, err)
	}
	data, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", "opencode-transcript-*.json")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	return []adapter.BundledFile{{AbsPath: f.Name(), RelPath: adapter.BundleRoot + "/transcript.json", Transient: true}}, nil
}
