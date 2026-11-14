package mcphttp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/MunifTanjim/argus/internal/atomicfile"
	"github.com/MunifTanjim/argus/internal/clienttoken"
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
		if _, _, err := atomicfile.RestrictToOwner(path); err != nil {
			return "", fmt.Errorf("restrict mcp token: %w", err)
		}
		return t, nil
	}
	if token, err = clienttoken.GenerateToken(); err != nil {
		return "", fmt.Errorf("generate mcp token: %w", err)
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
