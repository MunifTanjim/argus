// Package invite implements invites for onboarding into a locked network.
package invite

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/fxamacker/cbor/v2"

	"github.com/MunifTanjim/argus/internal/keyfmt"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

const BlobPrefix = "argus-invite:"

type Blob struct {
	GatewayURL string `cbor:"1,keyasint"`
	Token      string `cbor:"2,keyasint"`
	Genesis    []byte `cbor:"3,keyasint"`
	IssuerID   string `cbor:"4,keyasint"`
	IssuerKey  []byte `cbor:"5,keyasint"`
	InviteID   []byte `cbor:"6,keyasint"`
	Secret     []byte `cbor:"7,keyasint"`
}

var encMode = func() cbor.EncMode {
	em, err := cbor.CTAP2EncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	return em
}()

func (b Blob) Encode() (string, error) {
	raw, err := encMode.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("encode invite: %w", err)
	}
	return BlobPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeBlob(s string) (Blob, error) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	rest, ok := strings.CutPrefix(s, BlobPrefix)
	if !ok {
		return Blob{}, errors.New("invite: missing prefix " + BlobPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return Blob{}, fmt.Errorf("invite: bad base64: %w", err)
	}
	var b Blob
	if err := cbor.Unmarshal(raw, &b); err != nil {
		return Blob{}, fmt.Errorf("invite: bad cbor: %w", err)
	}
	if err := b.validate(); err != nil {
		return Blob{}, err
	}
	return b, nil
}

func (b Blob) validate() error {
	if b.GatewayURL == "" {
		return errors.New("invite: empty gateway url")
	}
	if b.IssuerID == "" {
		return errors.New("invite: empty issuer id")
	}
	for _, f := range []struct {
		name string
		got  []byte
		want int
	}{
		{"genesis", b.Genesis, trustpin.GenesisLen},
		{"issuer key", b.IssuerKey, keyfmt.Len},
		{"invite id", b.InviteID, 16},
		{"secret", b.Secret, 32},
	} {
		if len(f.got) != f.want {
			return fmt.Errorf("invite: %s must be %d bytes, got %d", f.name, f.want, len(f.got))
		}
	}
	return nil
}
