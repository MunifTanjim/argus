// Package trustpin owns a device's locked-mode trust root: where the trust-log
// genesis pin is stored, how a pin is resolved from config and disk, and the
// fail-closed state of a device that has seen a trust log it cannot verify.
//
// It deliberately sits above internal/trustlog, which stays a pure verifier: a
// trustlog Store is always constructed with a genesis learned out-of-band and
// never adopts one on its own.
package trustpin

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

// GenesisLen is the byte length of a trust-log genesis hash (BLAKE2s-256).
const GenesisLen = 32

// Source identifies where a resolved pin came from.
type Source int

const (
	SourceNone Source = iota
	SourceConfig
	SourceFile
)

func (s Source) String() string {
	switch s {
	case SourceConfig:
		return "config"
	case SourceFile:
		return "file"
	default:
		return "none"
	}
}

// Pin is a device's resolved locked-mode trust root. Genesis is nil when no pin
// is configured or persisted.
type Pin struct {
	Genesis []byte
	Source  Source
}

// File is the on-disk genesis pin for one device role (node or client).
type File struct{ path string }

func New(path string) *File { return &File{path: path} }

func (f *File) Path() string { return f.path }

// Load reads the persisted pin. An ABSENT file returns (nil, nil) — running
// without locked mode is legitimate. A file that EXISTS but is unreadable or is
// not GenesisLen bytes returns an error: a corrupt pin must fail closed rather
// than silently revert a locked device to open mode.
func (f *File) Load() ([]byte, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading genesis pin %s: %w", f.path, err)
	}
	if len(b) != GenesisLen {
		return nil, fmt.Errorf("genesis pin %s is %d bytes, want %d (corrupt)", f.path, len(b), GenesisLen)
	}
	return b, nil
}

func (f *File) Save(genesis []byte) error {
	if len(genesis) != GenesisLen {
		return fmt.Errorf("genesis is %d bytes, want %d", len(genesis), GenesisLen)
	}
	return atomicfile.Write(f.path, genesis)
}

// Clear removes the pin. An absent file is not an error.
func (f *File) Clear() error {
	if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Decode parses a base64 genesis hash as printed by `argus lock init`.
func Decode(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("genesis is not valid base64: %w", err)
	}
	if len(b) != GenesisLen {
		return nil, fmt.Errorf("genesis decoded to %d bytes, want %d", len(b), GenesisLen)
	}
	return b, nil
}

// Resolve applies pin precedence: config wins, then the persisted file, then
// unresolved. Config wins because it is the declarative source an operator
// deliberately wrote and must not be shadowed by a runtime adopt.
//
// A config pin and a file pin that disagree is a hard error. Silently preferring
// either is how a device ends up enforcing against a genesis nobody chose.
func Resolve(cfgGenesis string, f *File) (Pin, error) {
	var fromCfg []byte
	if cfgGenesis != "" {
		b, err := Decode(cfgGenesis)
		if err != nil {
			return Pin{}, fmt.Errorf("lock.genesis: %w", err)
		}
		fromCfg = b
	}
	fromFile, err := f.Load()
	if err != nil {
		return Pin{}, err
	}
	switch {
	case fromCfg != nil && fromFile != nil && !bytes.Equal(fromCfg, fromFile):
		return Pin{}, fmt.Errorf(
			"genesis pin conflict: lock.genesis is %s but %s holds %s; run `argus lock unpin` to drop the persisted pin",
			base64.StdEncoding.EncodeToString(fromCfg), f.path, base64.StdEncoding.EncodeToString(fromFile))
	case fromCfg != nil:
		return Pin{Genesis: fromCfg, Source: SourceConfig}, nil
	case fromFile != nil:
		return Pin{Genesis: fromFile, Source: SourceFile}, nil
	default:
		return Pin{Source: SourceNone}, nil
	}
}
