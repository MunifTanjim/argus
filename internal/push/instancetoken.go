package push

import (
	"os"
	"path/filepath"
	"strings"
)

// ReadInstanceToken returns the trimmed PushPort instance token stored at path,
// or "" when the file does not exist.
func ReadInstanceToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// WriteInstanceToken persists the token at path with 0600, creating the dir.
func WriteInstanceToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(token)), 0o600)
}
