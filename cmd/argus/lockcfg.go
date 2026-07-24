package main

import (
	"encoding/base64"
	"fmt"

	"github.com/MunifTanjim/argus/internal/config"
)

// lockGenesisHead decodes the configured base64 pinned genesis HEAD. It returns
// (nil, nil) when locked mode is unconfigured (empty lock.genesis), and an error
// when the value is present but not valid base64.
func lockGenesisHead(cfg *config.Config) ([]byte, error) {
	if cfg.Lock.Genesis == "" {
		return nil, nil
	}
	head, err := base64.StdEncoding.DecodeString(cfg.Lock.Genesis)
	if err != nil {
		return nil, fmt.Errorf("lock.genesis is not valid base64: %w", err)
	}
	if len(head) == 0 {
		return nil, fmt.Errorf("lock.genesis decoded to empty")
	}
	return head, nil
}
