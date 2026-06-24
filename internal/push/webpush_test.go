package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// TestEncryptWebPushRoundTrip encrypts a payload for a generated subscription and
// decrypts it back, exercising the full RFC 8291 / RFC 8188 path.
func TestEncryptWebPushRoundTrip(t *testing.T) {
	curve := ecdh.P256()
	uaPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ua key: %v", err)
	}
	uaPub := uaPriv.PublicKey().Bytes()
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}

	payload := []byte(`{"title":"argus","body":"hi"}`)
	body, err := encryptWebPush(b64.EncodeToString(uaPub), b64.EncodeToString(authSecret), payload)
	if err != nil {
		t.Fatalf("encryptWebPush: %v", err)
	}

	got := decryptWebPush(t, uaPriv, authSecret, body)
	if string(got) != string(payload) {
		t.Errorf("round-trip = %q, want %q", got, payload)
	}
}

// decryptWebPush reverses encryptWebPush as a real Web Push user agent would.
func decryptWebPush(t *testing.T, uaPriv *ecdh.PrivateKey, authSecret, body []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("body too short: %d", len(body))
	}
	salt := body[:16]
	idlen := int(body[20])
	asPub := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]

	curve := ecdh.P256()
	asKey, err := curve.NewPublicKey(asPub)
	if err != nil {
		t.Fatalf("as public: %v", err)
	}
	shared, err := uaPriv.ECDH(asKey)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}

	keyInfo := append([]byte("WebPush: info\x00"), uaPriv.PublicKey().Bytes()...)
	keyInfo = append(keyInfo, asPub...)
	ikm, err := hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32)
	if err != nil {
		t.Fatal(err)
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		t.Fatal(err)
	}
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	aead, _ := cipher.NewGCM(block)
	record, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("gcm open: %v", err)
	}
	// Strip the trailing 0x02 last-record delimiter (and any zero padding).
	i := len(record) - 1
	for i >= 0 && record[i] == 0x00 {
		i--
	}
	if i < 0 || record[i] != 0x02 {
		t.Fatalf("missing padding delimiter")
	}
	return record[:i]
}

func TestEncryptWebPushHeaderRecordSize(t *testing.T) {
	curve := ecdh.P256()
	uaPriv, _ := curve.GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	body, err := encryptWebPush(b64.EncodeToString(uaPriv.PublicKey().Bytes()),
		b64.EncodeToString(auth), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != 4096 {
		t.Errorf("record size = %d, want 4096", rs)
	}
	if body[20] != 65 {
		t.Errorf("keyid len = %d, want 65", body[20])
	}
}

func TestVAPIDAuthHeader(t *testing.T) {
	v, err := LoadOrCreateVAPID(t.TempDir() + "/vapid.pem")
	if err != nil {
		t.Fatalf("LoadOrCreateVAPID: %v", err)
	}
	h, err := v.authHeader("https://updates.push.services.mozilla.com/wpush/v1/abc", time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatalf("authHeader: %v", err)
	}
	if !strings.HasPrefix(h, "vapid t=") || !strings.Contains(h, ", k=") {
		t.Errorf("header = %q, want vapid t=.., k=..", h)
	}
	// JWT has three dot-separated segments.
	jwt := strings.TrimPrefix(strings.Split(h, ", k=")[0], "vapid t=")
	if len(strings.Split(jwt, ".")) != 3 {
		t.Errorf("jwt = %q, want 3 segments", jwt)
	}
}

// TestLoadOrCreateVAPIDPersists checks the key is stable across loads.
func TestLoadOrCreateVAPIDPersists(t *testing.T) {
	path := t.TempDir() + "/vapid.pem"
	a, err := LoadOrCreateVAPID(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateVAPID(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.pubB64 != b.pubB64 {
		t.Errorf("VAPID public key changed across loads: %q vs %q", a.pubB64, b.pubB64)
	}
}
