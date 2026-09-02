// Package keyfmt renders and parses the 32-byte values locked mode asks operators
// to copy between machines: signer keys, device keys, genesis hashes and
// disablement secrets.
//
// Every one of them is 32 bytes, so untagged encodings are interchangeable on
// sight and a value pasted into the wrong command parses happily and does the
// wrong thing. Each kind therefore carries a distinct prefix, and decoding is
// strict: a genesis hash handed to a command expecting a device key is an error
// naming both kinds, not a silent 32-byte match.
//
// The wire format is unaffected — these values travel as raw bytes in JSON. This
// package is only the human-facing spelling.
package keyfmt

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Len is the byte length of every value this package encodes.
const Len = 32

// Kind is one category of copyable 32-byte value.
type Kind struct {
	prefix string
	label  string
}

var (
	SignerKey   = Kind{"sigpub:", "signer key"}
	DeviceKey   = Kind{"devpub:", "device key"}
	Genesis     = Kind{"gen:", "genesis hash"}
	Disablement = Kind{"dis:", "disablement secret"}
	// Tip is a chain tip, printed for out-of-band comparison across nodes. It is
	// the same shape as Genesis — both are entry hashes — but tagged separately so
	// an audit tip cannot be pinned as a trust root.
	Tip = Kind{"tip:", "chain tip"}
)

var kinds = []Kind{SignerKey, DeviceKey, Genesis, Disablement, Tip}

// Prefix is the tag this kind's strings start with, including the colon.
func (k Kind) Prefix() string { return k.prefix }

// Label is the human name used in errors and help text.
func (k Kind) Label() string { return k.label }

// Encode renders b as prefix + lowercase hex.
func (k Kind) Encode(b []byte) string { return k.prefix + hex.EncodeToString(b) }

// Is reports whether s is spelled as this kind. It does not validate the body.
func (k Kind) Is(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), k.prefix) }

// Decode parses s as this kind. A value tagged as a different known kind is
// rejected by name rather than by length, which is what stops a genesis hash from
// being authorized as a device.
func (k Kind) Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if !k.Is(s) {
		if other, ok := kindOf(s); ok {
			return nil, fmt.Errorf("expected a %s (%s), got a %s (%s)", k.label, k.prefix, other.label, other.prefix)
		}
		return nil, fmt.Errorf("expected a %s of the form %s<%d hex chars>, got %q", k.label, k.prefix, Len*2, s)
	}
	b, err := hex.DecodeString(strings.TrimPrefix(s, k.prefix))
	if err != nil {
		return nil, fmt.Errorf("%s is not valid hex: %w", k.label, err)
	}
	if len(b) != Len {
		return nil, fmt.Errorf("%s is %d bytes, want %d", k.label, len(b), Len)
	}
	return b, nil
}

// DecodeAny parses s as whichever of want it is tagged as. It exists for the one
// input that legitimately takes more than one kind: a fork point, which may be any
// entry hash including the genesis.
func DecodeAny(s string, want ...Kind) ([]byte, error) {
	for _, k := range want {
		if k.Is(s) {
			return k.Decode(s)
		}
	}
	forms := make([]string, len(want))
	for i, k := range want {
		forms[i] = k.Prefix()
	}
	if other, ok := kindOf(s); ok {
		return nil, fmt.Errorf("expected %s, got a %s (%s)", strings.Join(forms, " or "), other.label, other.prefix)
	}
	return nil, fmt.Errorf("expected %s followed by %d hex chars, got %q", strings.Join(forms, " or "), Len*2, strings.TrimSpace(s))
}

// Tagged reports whether s carries any known kind's prefix. Callers that accept
// either a node name or a key use it to decide which way to parse, so that a
// mistyped key surfaces as a key error instead of an unknown-node error.
func Tagged(s string) bool {
	_, ok := kindOf(s)
	return ok
}

func kindOf(s string) (Kind, bool) {
	s = strings.TrimSpace(s)
	for _, k := range kinds {
		if strings.HasPrefix(s, k.prefix) {
			return k, true
		}
	}
	return Kind{}, false
}
