package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// b64 is the unpadded base64url encoding Web Push uses throughout.
var b64 = base64.RawURLEncoding

// encryptWebPush encrypts payload for a Web Push subscription per RFC 8291 using
// the aes128gcm content coding (RFC 8188). p256dhB64/authB64 are the subscription
// keys (base64url, unpadded) from the device. It returns the HTTP request body.
func encryptWebPush(p256dhB64, authB64 string, payload []byte) ([]byte, error) {
	uaPub, err := b64.DecodeString(p256dhB64)
	if err != nil {
		return nil, fmt.Errorf("push: decode p256dh: %w", err)
	}
	authSecret, err := b64.DecodeString(authB64)
	if err != nil {
		return nil, fmt.Errorf("push: decode auth: %w", err)
	}

	curve := ecdh.P256()
	uaKey, err := curve.NewPublicKey(uaPub)
	if err != nil {
		return nil, fmt.Errorf("push: subscription public key: %w", err)
	}
	asPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	asPub := asPriv.PublicKey().Bytes() // 65-byte uncompressed P-256 point
	shared, err := asPriv.ECDH(uaKey)
	if err != nil {
		return nil, err
	}

	// RFC 8291 §3.4: derive the input keying material from the ECDH secret.
	keyInfo := append([]byte("WebPush: info\x00"), uaPub...)
	keyInfo = append(keyInfo, asPub...)
	ikm, err := hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}

	// RFC 8188 aes128gcm: per-message salt, content-encryption key, and nonce.
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// Single record: payload followed by the 0x02 last-record delimiter.
	record := append(append([]byte{}, payload...), 0x02)
	ciphertext := aead.Seal(nil, nonce, record, nil)

	// Header: salt(16) | record-size(4, BE) | keyid-len(1) | keyid(as_public).
	const recordSize = 4096
	body := make([]byte, 0, 16+4+1+len(asPub)+len(ciphertext))
	body = append(body, salt...)
	rs := make([]byte, 4)
	binary.BigEndian.PutUint32(rs, recordSize)
	body = append(body, rs...)
	body = append(body, byte(len(asPub)))
	body = append(body, asPub...)
	body = append(body, ciphertext...)
	return body, nil
}

// VAPID signs the VAPID Authorization header (RFC 8292) for Web Push. The key is
// self-generated and persisted; it is not a Google credential.
type VAPID struct {
	priv   *ecdsa.PrivateKey
	pubB64 string // uncompressed public point, base64url (the "k" parameter)
}

// LoadOrCreateVAPID loads the persisted VAPID key, generating and saving one on
// first use.
func LoadOrCreateVAPID(path string) (*VAPID, error) {
	if b, err := os.ReadFile(path); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if k, e := x509.ParseECPrivateKey(blk.Bytes); e == nil {
				return newVAPID(k), nil
			}
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return newVAPID(k), nil
}

// PublicKey returns the VAPID public key (base64url, uncompressed P-256 point) —
// the applicationServerKey a Web Push device subscribes with.
func (v *VAPID) PublicKey() string { return v.pubB64 }

func newVAPID(k *ecdsa.PrivateKey) *VAPID {
	x := k.PublicKey.X.FillBytes(make([]byte, 32))
	y := k.PublicKey.Y.FillBytes(make([]byte, 32))
	pub := append([]byte{0x04}, append(x, y...)...)
	return &VAPID{priv: k, pubB64: b64.EncodeToString(pub)}
}

// authHeader builds the "vapid t=<jwt>, k=<pub>" Authorization value for endpoint.
func (v *VAPID) authHeader(endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(12 * time.Hour).Unix(),
		"sub": "mailto:argus@localhost",
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, v.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	jwt := signingInput + "." + b64.EncodeToString(sig)
	return "vapid t=" + jwt + ", k=" + v.pubB64, nil
}
