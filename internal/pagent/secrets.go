package pagent

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/db/gen"
)

const secretKeySize = 32

var secretNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// ValidSecretName reports whether name can be an environment variable name.
func ValidSecretName(name string) bool { return secretNameRe.MatchString(name) }

// SecretBox encrypts secret values with the node's key.
type SecretBox struct{ aead cipher.AEAD }

// OpenSecretBox loads the key at path, creating it (32 random bytes, mode
// 0600) when it does not exist. A key readable by others is refused.
func OpenSecretBox(path string) (*SecretBox, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		key, err = createKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secret key %s must be readable only by you (mode 0600)", path)
	}
	if len(key) != secretKeySize {
		return nil, fmt.Errorf("secret key %s: want %d bytes, got %d", path, secretKeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretBox{aead: aead}, nil
}

func createKey(path string) ([]byte, error) {
	key := make([]byte, secretKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return key, f.Close()
}

// Seal encrypts value; the agent id and name are bound to it, so a value
// can't be moved to another secret.
func (b *SecretBox) Seal(agentID, name, value string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(value), []byte(agentID+"\x00"+name)), nil
}

// Open decrypts what Seal returned.
func (b *SecretBox) Open(agentID, name string, enc []byte) (string, error) {
	n := b.aead.NonceSize()
	if len(enc) < n {
		return "", errors.New("secret: value too short")
	}
	out, err := b.aead.Open(nil, enc[:n], enc[n:], []byte(agentID+"\x00"+name))
	if err != nil {
		return "", fmt.Errorf("secret %s: cannot decrypt: %w", name, err)
	}
	return string(out), nil
}

// Secret is one stored secret of an agent. Value is set only when it was
// decrypted.
type Secret struct {
	AgentID, Name, Description string
	Value                      string
	UpdatedAt                  time.Time
	enc                        []byte
}

// SetSecret stores an encrypted value under name, replacing any earlier one.
func (s *Store) SetSecret(ctx context.Context, agentID, name, description string, enc []byte) error {
	return s.q.UpsertPAgentSecret(ctx, gen.UpsertPAgentSecretParams{
		AgentID: agentID, Name: name, Description: description, ValueEnc: enc, UpdatedAt: s.now().UnixNano(),
	})
}

// Secrets lists the agent's secrets by name, without values.
func (s *Store) Secrets(ctx context.Context, agentID string) ([]Secret, error) {
	rows, err := s.q.ListPAgentSecrets(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]Secret, len(rows))
	for i, r := range rows {
		out[i] = Secret{AgentID: r.AgentID, Name: r.Name, Description: r.Description, UpdatedAt: fromNanos(r.UpdatedAt), enc: r.ValueEnc}
	}
	return out, nil
}

// SecretValues lists the agent's secrets with their values decrypted.
// Secrets that don't decrypt are left out and reported in the error.
func (s *Store) SecretValues(ctx context.Context, b *SecretBox, agentID string) ([]Secret, error) {
	list, err := s.Secrets(ctx, agentID)
	if err != nil {
		return nil, err
	}
	var errs []error
	out := list[:0]
	for _, sec := range list {
		v, err := b.Open(agentID, sec.Name, sec.enc)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sec.Value = v
		out = append(out, sec)
	}
	return out, errors.Join(errs...)
}

// DeleteSecret removes a secret.
func (s *Store) DeleteSecret(ctx context.Context, agentID, name string) error {
	return affected(s.q.DeletePAgentSecret(ctx, gen.DeletePAgentSecretParams{AgentID: agentID, Name: name}))
}

// minRedact is the shortest value Redact replaces: shorter ones would mark
// ordinary words.
const minRedact = 4

// Redact replaces the known secret values in text with «NAME», also where a
// value appears escaped inside a JSON string.
func Redact(text string, secrets []Secret) string {
	if text == "" || len(secrets) == 0 {
		return text
	}
	sorted := slices.Clone(secrets)
	slices.SortFunc(sorted, func(a, b Secret) int { return len(b.Value) - len(a.Value) })
	for _, sec := range sorted {
		if len(sec.Value) < minRedact {
			continue
		}
		mark := "«" + sec.Name + "»"
		text = strings.ReplaceAll(text, sec.Value, mark)
		if q, err := json.Marshal(sec.Value); err == nil {
			if esc := string(q[1 : len(q)-1]); esc != sec.Value {
				text = strings.ReplaceAll(text, esc, mark)
			}
		}
	}
	return text
}

// Unredact puts the values of known secrets back where Redact marked them.
func Unredact(text string, secrets []Secret) string {
	for _, sec := range secrets {
		text = strings.ReplaceAll(text, "«"+sec.Name+"»", sec.Value)
	}
	return text
}
