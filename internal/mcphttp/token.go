package mcphttp

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var tokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LoadOrCreateToken returns the bearer token stored at path. A missing or
// malformed file is replaced with a new random token (mode 0600), written atomically.
func LoadOrCreateToken(path string) (token string, err error) {
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if t := strings.TrimSpace(string(b)); tokenPattern.MatchString(t) {
			if err := restrictMode(path); err != nil {
				return "", err
			}
			return t, nil
		}
	case !os.IsNotExist(err):
		return "", fmt.Errorf("read mcp token: %w", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate mcp token: %w", err)
	}
	token = hex.EncodeToString(raw)
	if err := writeAtomic(path, []byte(token+"\n")); err != nil {
		return "", err
	}
	return token, nil
}

func restrictMode(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat mcp token: %w", err)
	}
	if fi.Mode().Perm()&0o077 == 0 {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod mcp token: %w", err)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create mcp token dir: %w", err)
	}
	f, err := os.CreateTemp(dir, ".mcp.token-*")
	if err != nil {
		return fmt.Errorf("create mcp token: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write mcp token: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync mcp token: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close mcp token: %w", err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("install mcp token: %w", err)
	}
	return nil
}
