package mcphttp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

var tokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LoadOrCreateToken returns the bearer token stored at path. A missing or
// malformed file is replaced with a new random token (mode 0600), written
// atomically; concurrent first callers agree on one token.
func LoadOrCreateToken(path string) (token string, err error) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read mcp token: %w", err)
	}
	exists := err == nil
	if t := strings.TrimSpace(string(b)); exists && tokenPattern.MatchString(t) {
		if err := restrictMode(path); err != nil {
			return "", err
		}
		return t, nil
	}
	if token, err = newToken(); err != nil {
		return "", err
	}
	write := atomicfile.Create
	if exists {
		write = atomicfile.Write
	}
	switch err := write(path, []byte(token+"\n")); {
	case err == nil:
		return token, nil
	case errors.Is(err, fs.ErrExist):
		return LoadOrCreateToken(path)
	default:
		return "", fmt.Errorf("write mcp token: %w", err)
	}
}

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate mcp token: %w", err)
	}
	return hex.EncodeToString(raw), nil
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
