// Package e2e provides the end-to-end encrypted channel primitive used between
// an argus client and node while the gateway blindly relays frames. It wraps
// github.com/flynn/noise (Noise IK, Curve25519/ChaCha20-Poly1305/BLAKE2s) so no
// other package imports the noise library directly.
package e2e

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/flynn/noise"

	"github.com/MunifTanjim/argus/internal/keyfile"
)

var suite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

// KeyPair is a Curve25519 keypair. Private and Public are each 32 bytes.
type KeyPair struct {
	Private []byte
	Public  []byte
}

func GenerateKeyPair() (KeyPair, error) {
	dh, err := suite.GenerateKeypair(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("e2e: generate keypair: %w", err)
	}
	return KeyPair{Private: dh.Private, Public: dh.Public}, nil
}

// LoadOrCreateIdentity loads a persisted Curve25519 identity keypair, generating and
// saving one on first use (0600 file under a 0700 dir). A stable identity lets a
// locked network authorize this device's key once (argus lock sign) and keep it
// valid across restarts.
func LoadOrCreateIdentity(path string) (KeyPair, error) {
	return keyfile.LoadOrCreate(path, "LoadOrCreateIdentity",
		GenerateKeyPair,
		func(kp KeyPair) (priv, pub []byte) { return kp.Private, kp.Public },
		func(priv, pub []byte) (KeyPair, bool) {
			if len(priv) != 32 || len(pub) != 32 {
				return KeyPair{}, false
			}
			return KeyPair{Private: priv, Public: pub}, true
		},
	)
}

// maxChunk is the largest plaintext per Noise record: the 65535-byte record
// ceiling (noise.MaxMsgLen) minus the 16-byte Poly1305 tag.
const maxChunk = noise.MaxMsgLen - 16

// ErrUnknownInvite is returned by RespondInvite when the lookup rejects the
// invite payload carried in msg1.
var ErrUnknownInvite = errors.New("e2e: unknown invite")

// pskMode selects plain IK or IKpsk2 (PSK mixed at placement 2).
type pskMode bool

const (
	noPSK pskMode = false
	psk2  pskMode = true
)

func (m pskMode) pattern() string {
	if m == psk2 {
		return "IKpsk2"
	}
	return "IK"
}

func (m pskMode) role(role string) string {
	if m == psk2 {
		return "invite " + role
	}
	return role
}

// newHandshake builds the Noise IK handshake state shared by both roles. peerStatic
// is the responder's known static key for the initiator, and nil for the responder
// (which learns the initiator's static from msg1). For psk2 the caller sets the
// key with SetPresharedKey: the initiator up front, the responder once the invite
// is looked up.
func newHandshake(static KeyPair, prologue, peerStatic []byte, initiator bool, mode pskMode) (*noise.HandshakeState, error) {
	cfg := noise.Config{
		CipherSuite:   suite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeIK,
		Initiator:     initiator,
		Prologue:      prologue,
		StaticKeypair: noise.DHKey{Private: static.Private, Public: static.Public},
		PeerStatic:    peerStatic,
	}
	if mode == psk2 {
		cfg.PresharedKeyPlacement = 2
	}
	return noise.NewHandshakeState(cfg)
}

// Initiator holds the client-side handshake state between NewInitiator and Finish.
type Initiator struct {
	hs *noise.HandshakeState
}

// NewInitiator starts a Noise IK handshake as the initiator (client). peerPublic
// is the responder's (node's) static public key, learned from the roster.
func NewInitiator(static KeyPair, peerPublic, prologue []byte) (*Initiator, []byte, error) {
	return initiate(static, peerPublic, prologue, noPSK, nil, nil)
}

// NewInviteInitiator starts a Noise IKpsk2 handshake as the initiator. psk is the
// invite secret and payload (the invite ID) is carried in msg1 for the responder
// to look up the matching psk.
func NewInviteInitiator(static KeyPair, peerPublic, prologue, psk, payload []byte) (*Initiator, []byte, error) {
	return initiate(static, peerPublic, prologue, psk2, psk, payload)
}

func initiate(static KeyPair, peerPublic, prologue []byte, mode pskMode, psk, payload []byte) (*Initiator, []byte, error) {
	hs, err := newHandshake(static, prologue, peerPublic, true, mode)
	if err == nil && mode == psk2 {
		err = hs.SetPresharedKey(psk)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("e2e: new %s: %w", mode.role("initiator"), err)
	}
	msg1, cs0, cs1, err := hs.WriteMessage(nil, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("e2e: write msg1: %w", err)
	}
	if cs0 != nil || cs1 != nil {
		return nil, nil, fmt.Errorf("e2e: %s handshake completed after one message", mode.pattern())
	}
	return &Initiator{hs: hs}, msg1, nil
}

func (i *Initiator) Finish(msg2 []byte) (*Session, error) {
	_, cs0, cs1, err := i.hs.ReadMessage(nil, msg2)
	if err != nil {
		return nil, fmt.Errorf("e2e: read msg2: %w", err)
	}
	if cs0 == nil || cs1 == nil {
		return nil, errors.New("e2e: handshake incomplete after msg2")
	}
	// Initiator: cs0 encrypts to peer, cs1 decrypts from peer.
	return &Session{enc: cs0, dec: cs1}, nil
}

// Respond runs the responder (node) side of a Noise IK handshake: it consumes the
// initiator's msg1 and returns the completed Session, the initiator's authenticated
// static public key (for locked-mode authorization), and msg2 to send back.
func Respond(static KeyPair, prologue, msg1 []byte) (*Session, []byte, []byte, error) {
	sess, clientStatic, _, msg2, err := respond(static, prologue, msg1, noPSK, nil)
	return sess, clientStatic, msg2, err
}

// RespondInvite runs the responder side of a Noise IKpsk2 handshake. It reads msg1,
// passes the msg1 payload (invite ID) to lookup to obtain the psk, and returns
// ErrUnknownInvite without writing msg2 when lookup reports !ok. A wrong psk is
// detected by the initiator when it reads msg2.
func RespondInvite(static KeyPair, prologue, msg1 []byte, lookup func(payload []byte) (psk []byte, ok bool)) (sess *Session, clientStatic, payload, msg2 []byte, err error) {
	return respond(static, prologue, msg1, psk2, lookup)
}

// respond reads msg1 and writes msg2; for psk2, lookup maps the msg1 payload to
// the psk.
func respond(static KeyPair, prologue, msg1 []byte, mode pskMode, lookup func([]byte) ([]byte, bool)) (*Session, []byte, []byte, []byte, error) {
	hs, err := newHandshake(static, prologue, nil, false, mode)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("e2e: new %s: %w", mode.role("responder"), err)
	}
	payload, cs0, cs1, err := hs.ReadMessage(nil, msg1)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("e2e: read msg1: %w", err)
	}
	if cs0 != nil || cs1 != nil {
		return nil, nil, nil, nil, errors.New("e2e: handshake completed after msg1")
	}
	if mode == psk2 {
		psk, ok := lookup(payload)
		if !ok {
			return nil, nil, nil, nil, ErrUnknownInvite
		}
		if err := hs.SetPresharedKey(psk); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("e2e: set invite psk: %w", err)
		}
	}
	clientStatic := hs.PeerStatic() // authenticated initiator static (IK transmits it in msg1)
	msg2, cs0, cs1, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("e2e: write msg2: %w", err)
	}
	if cs0 == nil || cs1 == nil {
		return nil, nil, nil, nil, errors.New("e2e: handshake incomplete after msg2")
	}
	// Responder: cs0 decrypts from peer, cs1 encrypts to peer (swapped vs initiator).
	return &Session{enc: cs1, dec: cs0}, clientStatic, payload, msg2, nil
}

// Session is an established E2E channel: enc encrypts outbound messages, dec
// decrypts inbound ones. Messages MUST be processed in order per direction so
// the implicit Noise nonces stay aligned.
type Session struct {
	enc *noise.CipherState
	dec *noise.CipherState
}

// recordAD binds each Noise record to its position: the 0-based record index and
// a final-record flag, as AEAD associated data. This makes trailing-record
// truncation and empty/replaced blobs fail to open — the cleartext length
// prefixes alone are unauthenticated.
func recordAD(index uint32, final bool) []byte {
	ad := make([]byte, 5)
	binary.BigEndian.PutUint32(ad, index)
	if final {
		ad[4] = 1
	}
	return ad
}

// Seal encrypts one application message into an opaque blob of one or more
// length-prefixed Noise records (each record's ciphertext <= 65535 bytes). Each
// record authenticates its index and whether it is the final record, so a
// truncated or empty blob fails to Open. An empty message yields exactly one
// (final, empty-plaintext) record.
func (s *Session) Seal(plaintext []byte) ([]byte, error) {
	var out []byte
	var index uint32
	for {
		chunk := plaintext
		if len(chunk) > maxChunk {
			chunk = plaintext[:maxChunk]
		}
		plaintext = plaintext[len(chunk):]
		final := len(plaintext) == 0
		ct, err := s.enc.Encrypt(nil, recordAD(index, final), chunk)
		if err != nil {
			return nil, fmt.Errorf("e2e: encrypt: %w", err)
		}
		if len(ct) > 0xffff {
			return nil, errors.New("e2e: record exceeds 65535 bytes")
		}
		var hdr [2]byte
		binary.BigEndian.PutUint16(hdr[:], uint16(len(ct)))
		out = append(out, hdr[:]...)
		out = append(out, ct...)
		if final {
			return out, nil
		}
		index++
	}
}

// Open decrypts a blob of length-prefixed Noise records back into the original
// application message, verifying each record's index and that the blob ends with
// the record the sender marked final. A dropped trailing record or an empty blob
// fails authentication.
func (s *Session) Open(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, errors.New("e2e: empty blob")
	}
	var out []byte
	var index uint32
	for len(blob) > 0 {
		if len(blob) < 2 {
			return nil, errors.New("e2e: truncated record header")
		}
		n := int(binary.BigEndian.Uint16(blob[:2]))
		blob = blob[2:]
		if len(blob) < n {
			return nil, errors.New("e2e: truncated record body")
		}
		record := blob[:n]
		blob = blob[n:]
		final := len(blob) == 0
		pt, err := s.dec.Decrypt(nil, recordAD(index, final), record)
		if err != nil {
			return nil, fmt.Errorf("e2e: decrypt: %w", err)
		}
		out = append(out, pt...)
		index++
	}
	return out, nil
}
