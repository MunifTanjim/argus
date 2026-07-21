package main

import (
	"encoding/base64"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
)

func TestLockGenesisHead(t *testing.T) {
	// Unconfigured → (nil, nil), no error.
	if head, err := lockGenesisHead(&config.Config{}); err != nil || head != nil {
		t.Fatalf("empty: head=%v err=%v, want nil,nil", head, err)
	}

	// Valid base64 → decoded bytes.
	raw := []byte{1, 2, 3, 4}
	cfg := &config.Config{Lock: config.LockConfig{Genesis: base64.StdEncoding.EncodeToString(raw)}}
	head, err := lockGenesisHead(cfg)
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if string(head) != string(raw) {
		t.Fatalf("head = %v, want %v", head, raw)
	}

	// Malformed base64 → error.
	bad := &config.Config{Lock: config.LockConfig{Genesis: "not!base64!"}}
	if _, err := lockGenesisHead(bad); err == nil {
		t.Fatal("malformed genesis should error")
	}
}
