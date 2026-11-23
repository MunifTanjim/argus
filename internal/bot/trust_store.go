package bot

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/db/gen"
)

// Trust returns the content hashes recorded for repo, keyed by file name.
// ok is false when nothing has been recorded yet.
func (s *Store) Trust(ctx context.Context, repo string) (map[string]string, bool, error) {
	raw, err := s.q.GetBotTrust(ctx, repo)
	items, ok, err := one(raw, err, func(v string) string { return v })
	if err != nil || !ok {
		return nil, ok, err
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(items), &m); err != nil {
		return nil, false, fmt.Errorf("bot: trust %s: %w", repo, err)
	}
	return m, true, nil
}

// SetTrust records items as the trusted content hashes of repo.
func (s *Store) SetTrust(ctx context.Context, repo string, items map[string]string) error {
	if items == nil {
		items = map[string]string{}
	}
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return s.q.UpsertBotTrust(ctx, gen.UpsertBotTrustParams{Repo: repo, Items: string(data), UpdatedAt: s.now().UnixNano()})
}
