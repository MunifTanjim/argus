package e2e

import (
	"crypto/ed25519"
	"crypto/hmac"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash"
	"os"
	"testing"

	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/flynn/noise"
	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/chacha20poly1305"
)

// fixedReader yields a fixed byte sequence as "randomness" so ephemeral keys are
// deterministic during vector generation.
type fixedReader struct {
	b   []byte
	off int
}

func (r *fixedReader) Read(p []byte) (int, error) {
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func fixedKeypair(suite noise.CipherSuite, priv []byte) noise.DHKey {
	dh, err := suite.GenerateKeypair(&fixedReader{b: priv})
	if err != nil {
		panic(err)
	}
	return dh
}

func newBlake2s() hash.Hash { h, _ := blake2s.New256(nil); return h }

func hmacBlake2s(key, data []byte) []byte {
	m := hmac.New(newBlake2s, key)
	m.Write(data)
	return m.Sum(nil)
}

// noiseHKDF mirrors the Noise HKDF (HMAC-based) used by MixKey/Split.
func noiseHKDF(ck, ikm []byte, n int) [][]byte {
	tmp := hmacBlake2s(ck, ikm)
	o1 := hmacBlake2s(tmp, []byte{0x01})
	if n == 2 {
		return [][]byte{o1, hmacBlake2s(tmp, append(append([]byte{}, o1...), 0x02))}
	}
	o2 := hmacBlake2s(tmp, append(append([]byte{}, o1...), 0x02))
	o3 := hmacBlake2s(tmp, append(append([]byte{}, o2...), 0x03))
	return [][]byte{o1, o2, o3}
}

func TestGenerateVectors(t *testing.T) {
	if os.Getenv("GEN_E2E_VECTORS") == "" {
		t.Skip("set GEN_E2E_VECTORS=1 to regenerate app/test/e2e/testdata/vectors.json")
	}

	// Fixed 32-byte seeds (distinct, non-trivial).
	initStaticSeed := bytesFill(0x11)
	respStaticSeed := bytesFill(0x22)
	initEphSeed := bytesFill(0x33)
	respEphSeed := bytesFill(0x44)
	prologue := []byte("argus-e2e/v1|n1|c1")

	initStatic := fixedKeypair(suite, initStaticSeed)
	respStatic := fixedKeypair(suite, respStaticSeed)
	initEph := fixedKeypair(suite, initEphSeed)
	respEph := fixedKeypair(suite, respEphSeed)

	// Initiator writes msg1 with a fixed ephemeral.
	ihs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite, Random: &fixedReader{b: initEphSeed}, Pattern: noise.HandshakeIK,
		Initiator: true, Prologue: prologue,
		StaticKeypair: initStatic, PeerStatic: respStatic.Public,
	})
	must(t, err)
	msg1, _, _, err := ihs.WriteMessage(nil, nil)
	must(t, err)

	// Responder reads msg1, writes msg2 with a fixed ephemeral.
	rhs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite, Random: &fixedReader{b: respEphSeed}, Pattern: noise.HandshakeIK,
		Initiator: false, Prologue: prologue, StaticKeypair: respStatic,
	})
	must(t, err)
	_, _, _, err = rhs.ReadMessage(nil, msg1)
	must(t, err)
	msg2, rcs0, rcs1, err := rhs.WriteMessage(nil, nil)
	must(t, err)

	// Initiator reads msg2 -> split.
	_, ics0, ics1, err := ihs.ReadMessage(nil, msg2)
	must(t, err)

	// Real e2e.Session wrappers (this file is package e2e).
	initSess := &Session{enc: ics0, dec: ics1} // initiator: enc=cs0, dec=cs1
	respSess := &Session{enc: rcs1, dec: rcs0} // responder: enc=cs1, dec=cs0

	samplePlaintexts := [][]byte{
		{},
		[]byte("hello argus"),
		bytesFill2(0x5a, maxChunk*2+7), // multi-record (3 records)
	}
	var sealSamples, openSamples []map[string]string
	for _, pt := range samplePlaintexts {
		sealed, err := initSess.Seal(pt)
		must(t, err)
		sealSamples = append(sealSamples, map[string]string{"plaintext": b64(pt), "sealed": b64(sealed)})
	}
	for _, pt := range samplePlaintexts {
		sealed, err := respSess.Seal(pt)
		must(t, err)
		openSamples = append(openSamples, map[string]string{"plaintext": b64(pt), "sealed": b64(sealed)})
	}

	// Unit vectors from x/crypto directly.
	h0 := blake2sSum([]byte("Noise_IK_25519_ChaChaPoly_BLAKE2s"))
	hkIkm := bytesFill(0x77)
	hkCk := bytesFill(0x88)
	hkOut := noiseHKDF(hkCk, hkIkm, 2)
	hIn := bytesFill(0x99)
	hData := []byte("mixhash-data")
	hOut := blake2sSum(append(append([]byte{}, hIn...), hData...))

	cipherKey := bytesFill(0xab)
	var counter uint64 = 5
	cipherAD := []byte("ad-bytes")
	cipherPT := []byte("cipher plaintext sample")
	nonce := make([]byte, 12)
	binary.LittleEndian.PutUint64(nonce[4:], counter)
	aead, err := chacha20poly1305.New(cipherKey)
	must(t, err)
	cipherCT := aead.Seal(nil, nonce, cipherPT, cipherAD)

	// ---- F2 frame vectors: a fresh handshake (same seeds -> same keys, nonces at 0)
	// so a Dart session derived from the F1 handshake vectors matches from nonce 0.
	ihs2, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite, Random: &fixedReader{b: initEphSeed}, Pattern: noise.HandshakeIK,
		Initiator: true, Prologue: prologue, StaticKeypair: initStatic, PeerStatic: respStatic.Public,
	})
	must(t, err)
	fmsg1, _, _, err := ihs2.WriteMessage(nil, nil)
	must(t, err)
	rhs2, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite, Random: &fixedReader{b: respEphSeed}, Pattern: noise.HandshakeIK,
		Initiator: false, Prologue: prologue, StaticKeypair: respStatic,
	})
	must(t, err)
	if _, _, _, err = rhs2.ReadMessage(nil, fmsg1); err != nil {
		t.Fatal(err)
	}
	fmsg2, rcs0b, rcs1b, err := rhs2.WriteMessage(nil, nil)
	must(t, err)
	_, ics0b, ics1b, err := ihs2.ReadMessage(nil, fmsg2)
	must(t, err)
	initSess2 := &Session{enc: ics0b, dec: ics1b}
	respSess2 := &Session{enc: rcs1b, dec: rcs0b}

	const frameChanID = "c-frame"
	const frameNodeID = "node-A"

	// request body: initiator enc, nonce 0
	reqParams := []byte(`{"foo":"bar","n":7}`)
	reqBody, err := initSess2.Seal(reqParams)
	must(t, err)

	// inbound response: responder enc, nonce 0; inner {"result":<result>}
	respResult := []byte(`{"ok":true}`)
	respInner := []byte(`{"result":` + string(respResult) + `}`)
	respSealed, err := respSess2.Seal(respInner)
	must(t, err)
	respFrame := buildFrame(map[string]any{
		"jsonrpc": "2.0", "id": 42,
		"route": map[string]any{"chan_id": frameChanID},
		"body":  base64.StdEncoding.EncodeToString(respSealed),
	})

	// inbound notification: responder enc, nonce 1
	notifParams := []byte(`{"event":"tick","seq":1}`)
	notifSealed, err := respSess2.Seal(notifParams)
	must(t, err)
	notifFrame := buildFrame(map[string]any{
		"jsonrpc": "2.0", "method": "session.event",
		"route": map[string]any{"chan_id": frameChanID},
		"body":  base64.StdEncoding.EncodeToString(notifSealed),
	})

	// handshake frame: unsealed msg1
	hsFrame := buildFrame(map[string]any{
		"jsonrpc": "2.0", "method": "e2e.handshake",
		"route": map[string]any{"chan_id": frameChanID},
		"body":  base64.StdEncoding.EncodeToString(fmsg1),
	})

	// ---- F5 trust-log vectors (from the real trustlog package) ----
	tlSignerPriv := ed25519.NewKeyFromSeed(bytesFill(0xA1))
	tlSigner := trustlog.SignerKey{Public: tlSignerPriv.Public().(ed25519.PublicKey), Private: tlSignerPriv}
	tlDeviceA := bytesFill(0xD1)
	tlDeviceB := bytesFill(0xD2)
	tlSecret := bytesFill(0x5E)
	tlCommit := trustlog.DisablementCommitment(tlSecret)

	tlLog, err := trustlog.NewGenesis([][]byte{tlSigner.Public}, tlSigner, [][]byte{tlCommit})
	must(t, err)
	tlGenesisHash := tlLog.Tip()
	must(t, tlLog.AuthorizeDevice(tlDeviceA, tlSigner))
	tlChain := trustlog.MarshalChain(tlLog.Entries())
	tlHead := tlLog.Tip()

	// fork_chain: same genesis (same signer+disablements), diverges at entry 1 with device B.
	tlForkLog, err := trustlog.NewGenesis([][]byte{tlSigner.Public}, tlSigner, [][]byte{tlCommit})
	must(t, err)
	must(t, tlForkLog.AuthorizeDevice(tlDeviceB, tlSigner))
	tlForkChain := trustlog.MarshalChain(tlForkLog.Entries())

	tlLog2, err := trustlog.NewGenesis([][]byte{tlSigner.Public}, tlSigner, [][]byte{tlCommit})
	must(t, err)
	must(t, tlLog2.AuthorizeDevice(tlDeviceA, tlSigner))
	must(t, tlLog2.Disable(tlSecret, tlSigner))
	tlDisabledChain := trustlog.MarshalChain(tlLog2.Entries())
	tlDisabledHead := tlLog2.Tip()

	wPriv := ed25519.NewKeyFromSeed(bytesFill(0xB2))
	wSigner := trustlog.SignerKey{Public: wPriv.Public().(ed25519.PublicKey), Private: wPriv}
	wLog, err := trustlog.NewGenesis([][]byte{wSigner.Public}, wSigner, nil)
	must(t, err)
	must(t, wLog.AuthorizeDevice(tlDeviceA, wSigner))
	tlWrongChain := trustlog.MarshalChain(wLog.Entries())

	// ---- F5 enforcement vectors: use real X25519 keypair seeds so the Dart test
	// can do keyPairFromSeed(seed) and get a node whose publicKey == authorized device key.
	enfNodeASeed := bytesFill(0xE1)
	enfNodeBSeed := bytesFill(0xE2)
	enfNodeAPub := fixedKeypair(suite, enfNodeASeed).Public // X25519 pub for seed 0xE1*32
	enfSignerPriv := ed25519.NewKeyFromSeed(bytesFill(0xC1))
	enfSigner := trustlog.SignerKey{Public: enfSignerPriv.Public().(ed25519.PublicKey), Private: enfSignerPriv}
	enfLog, err := trustlog.NewGenesis([][]byte{enfSigner.Public}, enfSigner, nil)
	must(t, err)
	enfGenesisHash := enfLog.Tip()
	must(t, enfLog.AuthorizeDevice(enfNodeAPub, enfSigner))
	enfChain := trustlog.MarshalChain(enfLog.Entries())

	// Mid-session re-evaluation vectors: a chain authorizing BOTH node A and node B
	// (reusing the enforcement node seeds), then a monotonic extension revoking B.
	reevNodeBPub := fixedKeypair(suite, enfNodeBSeed).Public
	reevLog, err := trustlog.NewGenesis([][]byte{enfSigner.Public}, enfSigner, nil)
	must(t, err)
	must(t, reevLog.AuthorizeDevice(enfNodeAPub, enfSigner))
	must(t, reevLog.AuthorizeDevice(reevNodeBPub, enfSigner))
	reevInitialChain := trustlog.MarshalChain(reevLog.Entries())
	must(t, reevLog.RevokeDevice(reevNodeBPub, enfSigner))
	reevRevokeBChain := trustlog.MarshalChain(reevLog.Entries())

	// ---- signer-removal parity vector (Go↔Dart cross-language pin) ----
	// genesis trusts A,B → A authorizes devA → B authorizes devB → remove A (signed by B).
	// Expected after replay: devA unauthorized, devB authorized.
	srPrivA := ed25519.NewKeyFromSeed(bytesFill(0xA3))
	srPrivB := ed25519.NewKeyFromSeed(bytesFill(0xA4))
	srSignerA := trustlog.SignerKey{Public: srPrivA.Public().(ed25519.PublicKey), Private: srPrivA}
	srSignerB := trustlog.SignerKey{Public: srPrivB.Public().(ed25519.PublicKey), Private: srPrivB}
	srDevA := bytesFill(0xD3)
	srDevB := bytesFill(0xD4)
	srLog, err := trustlog.NewGenesis([][]byte{srSignerA.Public, srSignerB.Public}, srSignerA, nil)
	must(t, err)
	must(t, srLog.AuthorizeDevice(srDevA, srSignerA))
	must(t, srLog.AuthorizeDevice(srDevB, srSignerB))
	must(t, srLog.RemoveSigner(srSignerA.Public, srSignerB))
	srChain := trustlog.MarshalChain(srLog.Entries())

	sfSigners := [][]byte{bytesFill(0xC1), bytesFill(0xC2)}
	sfWords := trustlog.SignerSetFingerprint(sfSigners)

	out := map[string]any{
		"protocol_name":  "Noise_IK_25519_ChaChaPoly_BLAKE2s",
		"h0":             b64(h0),
		"prologue":       b64(prologue),
		"init_static":    map[string]string{"priv": b64(initStatic.Private), "pub": b64(initStatic.Public)},
		"resp_static":    map[string]string{"priv": b64(respStatic.Private), "pub": b64(respStatic.Public)},
		"init_ephemeral": map[string]string{"priv": b64(initEph.Private), "pub": b64(initEph.Public)},
		"resp_ephemeral": map[string]string{"priv": b64(respEph.Private), "pub": b64(respEph.Public)},
		"msg1":           b64(msg1),
		"msg2":           b64(msg2),
		"seal_samples":   sealSamples,
		"open_samples":   openSamples,
		"hkdf_sample":    map[string]any{"ck": b64(hkCk), "ikm": b64(hkIkm), "num": 2, "outputs": []string{b64(hkOut[0]), b64(hkOut[1])}},
		"mixhash_sample": map[string]string{"h_in": b64(hIn), "data": b64(hData), "h_out": b64(hOut)},
		"cipher_sample":  map[string]any{"key": b64(cipherKey), "counter": counter, "ad": b64(cipherAD), "plaintext": b64(cipherPT), "ciphertext": b64(cipherCT)},
		"frame_request": map[string]any{
			"chan_id": frameChanID, "node_id": frameNodeID, "id": 42, "method": "server.info",
			"params": b64(reqParams), "body": b64(reqBody),
		},
		"frame_inbound": []map[string]any{
			{"kind": "response", "frame": b64(respFrame), "result": b64(respResult)},
			{"kind": "notification", "frame": b64(notifFrame), "method": "session.event", "params": b64(notifParams)},
		},
		"frame_handshake": map[string]any{
			"chan_id": frameChanID, "handshake": b64(fmsg1), "frame": b64(hsFrame),
		},
		"signer_fingerprint": map[string]any{
			"signers": []string{b64(sfSigners[0]), b64(sfSigners[1])},
			"words":   sfWords,
		},
		"trustlog": map[string]any{
			"genesis_head": b64(tlGenesisHash), "chain": b64(tlChain), "head": b64(tlHead),
			"device_a": b64(tlDeviceA), "device_b": b64(tlDeviceB),
			"secret": b64(tlSecret), "commitment": b64(tlCommit),
			"disabled_chain": b64(tlDisabledChain), "disabled_head": b64(tlDisabledHead),
			"wrong_genesis_chain":      b64(tlWrongChain),
			"fork_chain":               b64(tlForkChain),
			"enforcement_genesis_head": b64(enfGenesisHash),
			"enforcement_chain":        b64(enfChain),
			"enforcement_node_a_seed":  b64(enfNodeASeed),
			"enforcement_node_b_seed":  b64(enfNodeBSeed),
			"reeval_initial_chain":     b64(reevInitialChain),
			"reeval_revoke_b_chain":    b64(reevRevokeBChain),
		},
		"signer_removal": map[string]any{
			// genesis A,B → A authorizes devA → B authorizes devB → remove A (signed by B).
			// After replay: devA unauthorized (A removed), devB authorized (B still trusted).
			"chain": b64(srChain),
			"dev_a": b64(srDevA),
			"dev_b": b64(srDevB),
		},
	}
	b, err := json.MarshalIndent(out, "", "  ")
	must(t, err)
	must(t, os.MkdirAll("../../app/test/e2e/testdata", 0o755))
	must(t, os.WriteFile("../../app/test/e2e/testdata/vectors.json", append(b, '\n'), 0o644))
}

// TestSignerRemovalGoldenVector loads the shared signer_removal section from the
// cross-language golden vector file and asserts Go replays it identically to Dart.
func TestSignerRemovalGoldenVector(t *testing.T) {
	raw, err := os.ReadFile("../../app/test/e2e/testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vectors.json: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal vectors.json: %v", err)
	}
	var sr map[string]string
	if err := json.Unmarshal(top["signer_removal"], &sr); err != nil {
		t.Fatalf("unmarshal signer_removal: %v", err)
	}
	chain, _ := base64.StdEncoding.DecodeString(sr["chain"])
	devA, _ := base64.StdEncoding.DecodeString(sr["dev_a"])
	devB, _ := base64.StdEncoding.DecodeString(sr["dev_b"])

	entries, err := trustlog.UnmarshalChain(chain)
	if err != nil {
		t.Fatalf("UnmarshalChain: %v", err)
	}
	tlog, err := trustlog.Load(entries)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if tlog.DeviceAuthorized(devA) {
		t.Error("devA must be unauthorized after its authorizing signer is removed")
	}
	if !tlog.DeviceAuthorized(devB) {
		t.Error("devB must remain authorized (its authorizing signer B is still trusted)")
	}
}

func buildFrame(m map[string]any) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func bytesFill(v byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = v
	}
	return b
}
func bytesFill2(v byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}
func blake2sSum(d []byte) []byte { s := blake2s.Sum256(d); return s[:] }
