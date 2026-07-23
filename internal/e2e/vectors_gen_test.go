package e2e

import (
	"bytes"
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

	// ---- fork-choice parity vectors ----
	// (a) co-signed-shorter-beats-longer-plain:
	//   cur: genesis(A,B,C) -> C authorizes dev1 -> C authorizes dev2 (longer, plain)
	//   cand: genesis(A,B,C) -> revoke-signer(C) co-signed by A,B (shorter, co-signed)
	//   expected winner: cand (revoke branch); C not trusted; dev1,dev2 not authorized.
	fcA := mustGenSignerSeed(t, 0xFA)
	fcB := mustGenSignerSeed(t, 0xFB)
	fcC := mustGenSignerSeed(t, 0xFC)
	fcPubs := [][]byte{fcA.Public, fcB.Public, fcC.Public}
	fcCur, err := trustlog.NewGenesis(fcPubs, fcA, nil)
	must(t, err)
	fcCand, err := trustlog.NewGenesis(fcPubs, fcA, nil)
	must(t, err)
	fcGenesisHash := fcCur.Tip()
	fcDev1 := bytesFill2(0xD1, 32)
	fcDev2 := bytesFill2(0xD2, 32)
	must(t, fcCur.AuthorizeDevice(fcDev1, fcC))
	must(t, fcCur.AuthorizeDevice(fcDev2, fcC))
	must(t, fcCand.RevokeSigner([][]byte{fcC.Public}, nil, []trustlog.SignerKey{fcA, fcB}))
	fcCurChain := trustlog.MarshalChain(fcCur.Entries())
	fcCandChain := trustlog.MarshalChain(fcCand.Entries())
	fcWinnerTip := fcCand.Tip()

	// (b) puppet-attack vector:
	//   honest: genesis(A,B,C) -> revoke-signer(C) co-signed by A,B
	//   attacker: genesis(A,B,C) -> addSigner(P1) -> addSigner(P2) -> addSigner(P3) ->
	//             revoke-signer(A,B) co-signed by C,P1,P2,P3
	//   expected winner: honest; A,B trusted; C,P1,P2,P3 not trusted.
	ppA := mustGenSignerSeed(t, 0xA5)
	ppB := mustGenSignerSeed(t, 0xA6)
	ppC := mustGenSignerSeed(t, 0xA7)
	ppP1 := mustGenSignerSeed(t, 0xA8)
	ppP2 := mustGenSignerSeed(t, 0xA9)
	ppP3 := mustGenSignerSeed(t, 0xAA)
	ppPubs := [][]byte{ppA.Public, ppB.Public, ppC.Public}
	ppHonest, err := trustlog.NewGenesis(ppPubs, ppA, nil)
	must(t, err)
	ppAttacker, err := trustlog.NewGenesis(ppPubs, ppA, nil)
	must(t, err)
	ppGenesisHash := ppHonest.Tip()
	must(t, ppHonest.RevokeSigner([][]byte{ppC.Public}, nil, []trustlog.SignerKey{ppA, ppB}))
	must(t, ppAttacker.AddSigner(ppP1.Public, ppC))
	must(t, ppAttacker.AddSigner(ppP2.Public, ppC))
	must(t, ppAttacker.AddSigner(ppP3.Public, ppC))
	must(t, ppAttacker.RevokeSigner([][]byte{ppA.Public, ppB.Public}, nil, []trustlog.SignerKey{ppC, ppP1, ppP2, ppP3}))
	ppHonestChain := trustlog.MarshalChain(ppHonest.Entries())
	ppAttackerChain := trustlog.MarshalChain(ppAttacker.Entries())
	ppWinnerTip := ppHonest.Tip()

	// (c) tie-break: two co-signed revoke branches with equal weight; winner = lowest tip hash.
	// In 2-entry chains (genesis + revokeSigner), the tip IS hashEntry(entries[1]), the first
	// diverging entry — so the winner has the lower tip.
	tbA := mustGenSignerSeed(t, 0xB5)
	tbB := mustGenSignerSeed(t, 0xB6)
	tbC := mustGenSignerSeed(t, 0xB7)
	tbD := mustGenSignerSeed(t, 0xB8)
	tbPubs := [][]byte{tbA.Public, tbB.Public, tbC.Public, tbD.Public}
	tbX, err := trustlog.NewGenesis(tbPubs, tbA, nil)
	must(t, err)
	tbY, err := trustlog.NewGenesis(tbPubs, tbA, nil)
	must(t, err)
	tbGenesisHash := tbX.Tip()
	must(t, tbX.RevokeSigner([][]byte{tbC.Public}, nil, []trustlog.SignerKey{tbA, tbB}))
	must(t, tbY.RevokeSigner([][]byte{tbD.Public}, nil, []trustlog.SignerKey{tbA, tbB}))
	tbXChain := trustlog.MarshalChain(tbX.Entries())
	tbYChain := trustlog.MarshalChain(tbY.Entries())
	// winner = branch with lower first-diverging-entry hash (= tip for 2-entry chains)
	tbXEntries, _ := trustlog.UnmarshalChain(tbXChain)
	tbYEntries, _ := trustlog.UnmarshalChain(tbYChain)
	tbXDivHash := trustlog.HashEntry(&tbXEntries[1])
	tbYDivHash := trustlog.HashEntry(&tbYEntries[1])
	tbWinnerTip := tbX.Tip()
	if bytes.Compare(tbYDivHash, tbXDivHash) < 0 {
		tbWinnerTip = tbY.Tip()
	}

	// (d) revoke-signer-with-replacement parity vector:
	//   c_branch: genesis(A,B,C) -> C authorizes devC (longer, plain)
	//   honest:   genesis(A,B,C) -> revoke-signer(C, replaces=D) co-signed by A,B (shorter, co-signed+replacement)
	//   expected winner: honest; A,B,D trusted; C not trusted; devC not authorized.
	rwrA := mustGenSignerSeed(t, 0xE4)
	rwrB := mustGenSignerSeed(t, 0xE5)
	rwrC := mustGenSignerSeed(t, 0xE6)
	rwrD := mustGenSignerSeed(t, 0xE7)
	rwrDevC := bytesFill(0xEC)
	rwrPubs := [][]byte{rwrA.Public, rwrB.Public, rwrC.Public}
	rwrHonest, err := trustlog.NewGenesis(rwrPubs, rwrA, nil)
	must(t, err)
	rwrCBranch, err := trustlog.NewGenesis(rwrPubs, rwrA, nil)
	must(t, err)
	rwrGenesisHash := rwrHonest.Tip()
	// c's branch: C authorizes devC (longer, plain — will lose)
	must(t, rwrCBranch.AuthorizeDevice(rwrDevC, rwrC))
	// honest: revoke C, add D as replacement, co-signed by A,B (fork from genesis — will win)
	must(t, rwrHonest.RevokeSigner([][]byte{rwrC.Public}, [][]byte{rwrD.Public}, []trustlog.SignerKey{rwrA, rwrB}))
	rwrHonestChain := trustlog.MarshalChain(rwrHonest.Entries())
	rwrCBranchChain := trustlog.MarshalChain(rwrCBranch.Entries())
	rwrWinnerTip := rwrHonest.Tip()

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
		"fork_choice": map[string]any{
			// (a) co-signed shorter branch beats longer plain branch.
			"cosigned_shorter_beats_longer": map[string]any{
				"genesis_hash":   b64(fcGenesisHash),
				"cur":            b64(fcCurChain),  // longer: C authorized dev1+dev2
				"cand":           b64(fcCandChain), // shorter: A,B co-sign revoke-signer(C)
				"winner_tip":     b64(fcWinnerTip), // cand must win
				"winner_signers": []string{b64(fcA.Public), b64(fcB.Public)},
				"winner_devices": []string{}, // dev1,dev2 must not be authorized
				// devices authorized by C are invalidated when C is revoked
				"loser_signer_not_trusted": b64(fcC.Public),
				"loser_dev_a":              b64(fcDev1),
				"loser_dev_b":              b64(fcDev2),
			},
			// (b) puppet-attack: attacker adds post-fork signers + high co-sign count, still loses.
			"puppet_attack": map[string]any{
				"genesis_hash":    b64(ppGenesisHash),
				"honest_chain":    b64(ppHonestChain),   // A,B co-sign revoke-signer(C)
				"attacker_chain":  b64(ppAttackerChain), // C adds P1,P2,P3 then {C,P1,P2,P3} co-sign revoke-signer(A,B)
				"winner_tip":      b64(ppWinnerTip),     // honest must win
				"winner_signer_a": b64(ppA.Public),      // A must stay trusted
				"winner_signer_b": b64(ppB.Public),      // B must stay trusted
				"loser_signer_c":  b64(ppC.Public),      // C must be revoked
				"puppet_p1":       b64(ppP1.Public),     // P1 must never be trusted
				"puppet_p2":       b64(ppP2.Public),
				"puppet_p3":       b64(ppP3.Public),
			},
			// (c) tie-break: two co-signed revokes with equal weight; winner = lowest tip hash.
			"tiebreak": map[string]any{
				"genesis_hash": b64(tbGenesisHash),
				"chain_x":      b64(tbXChain),
				"chain_y":      b64(tbYChain),
				"winner_tip":   b64(tbWinnerTip), // branch with lower first-diverging-entry hash
			},
			// (d) revoke-signer with replacement: co-signed+replacement beats plain longer branch.
			"revoke_with_replacement": map[string]any{
				"genesis_hash":    b64(rwrGenesisHash),
				"honest_chain":    b64(rwrHonestChain),  // A,B co-sign revoke C + add D; fork from genesis
				"c_branch":        b64(rwrCBranchChain), // C authorized devC (longer, plain)
				"winner_tip":      b64(rwrWinnerTip),    // honest must win
				"winner_signer_a": b64(rwrA.Public),     // A must stay trusted
				"winner_signer_b": b64(rwrB.Public),     // B must stay trusted
				"winner_signer_d": b64(rwrD.Public),     // D (replacement) must be trusted
				"loser_signer_c":  b64(rwrC.Public),     // C must not be trusted
				"loser_dev_c":     b64(rwrDevC),         // devC authorized by C must not be authorized
			},
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

// TestForkChoiceGoldenVectors loads the shared fork_choice section from vectors.json
// and asserts Go resolves each scenario identically, pinning Go↔Dart parity.
func TestForkChoiceGoldenVectors(t *testing.T) {
	raw, err := os.ReadFile("../../app/test/e2e/testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vectors.json: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal vectors.json: %v", err)
	}
	var fc map[string]json.RawMessage
	if err := json.Unmarshal(top["fork_choice"], &fc); err != nil {
		t.Fatalf("unmarshal fork_choice: %v", err)
	}

	// (a) co-signed shorter branch beats longer plain branch
	t.Run("cosigned_shorter_beats_longer", func(t *testing.T) {
		var v struct {
			GenesisHash           string `json:"genesis_hash"`
			Cur                   string `json:"cur"`
			Cand                  string `json:"cand"`
			WinnerTip             string `json:"winner_tip"`
			LoserSignerNotTrusted string `json:"loser_signer_not_trusted"`
			LoserDevA             string `json:"loser_dev_a"`
			LoserDevB             string `json:"loser_dev_b"`
		}
		must(t, json.Unmarshal(fc["cosigned_shorter_beats_longer"], &v))
		gh := decodeB64(t, v.GenesisHash)
		cur := decodeB64(t, v.Cur)
		cand := decodeB64(t, v.Cand)
		wantTip := decodeB64(t, v.WinnerTip)
		loserSigner := decodeB64(t, v.LoserSignerNotTrusted)
		devA := decodeB64(t, v.LoserDevA)
		devB := decodeB64(t, v.LoserDevB)

		s := trustlog.NewStore(gh)
		// ingest longer plain branch first
		if err := s.Ingest(cur); err != nil {
			t.Fatalf("ingest cur: %v", err)
		}
		// then ingest the shorter co-signed branch — must be adopted
		if err := s.Ingest(cand); err != nil {
			t.Fatalf("shorter co-signed branch must be adopted: %v", err)
		}
		if !bytes.Equal(s.Tip(), wantTip) {
			t.Error("winner tip mismatch")
		}
		if s.SignerTrusted(loserSigner) {
			t.Error("revoked signer must not be trusted after winning branch adopted")
		}
		if s.DeviceAuthorized(devA) || s.DeviceAuthorized(devB) {
			t.Error("devices authorized by revoked signer must not be authorized")
		}
	})

	// (b) puppet-attack: attacker's higher co-sign count still loses
	t.Run("puppet_attack", func(t *testing.T) {
		var v map[string]string
		must(t, json.Unmarshal(fc["puppet_attack"], &v))
		gh := decodeB64(t, v["genesis_hash"])
		honest := decodeB64(t, v["honest_chain"])
		attacker := decodeB64(t, v["attacker_chain"])
		wantTip := decodeB64(t, v["winner_tip"])
		sigA := decodeB64(t, v["winner_signer_a"])
		sigB := decodeB64(t, v["winner_signer_b"])
		sigC := decodeB64(t, v["loser_signer_c"])
		p1 := decodeB64(t, v["puppet_p1"])
		p2 := decodeB64(t, v["puppet_p2"])
		p3 := decodeB64(t, v["puppet_p3"])

		for _, order := range [][2][]byte{{honest, attacker}, {attacker, honest}} {
			s := trustlog.NewStore(gh)
			if err := s.Ingest(order[0]); err != nil {
				t.Fatalf("ingest first: %v", err)
			}
			if err := s.Ingest(order[1]); err != nil {
				t.Fatalf("ingest second: %v", err)
			}
			if !bytes.Equal(s.Tip(), wantTip) {
				t.Error("puppet attack: winner tip mismatch")
			}
			if !s.SignerTrusted(sigA) || !s.SignerTrusted(sigB) {
				t.Error("honest signers a,b must stay trusted")
			}
			if s.SignerTrusted(sigC) || s.SignerTrusted(p1) || s.SignerTrusted(p2) || s.SignerTrusted(p3) {
				t.Error("compromised signer c and puppets must not be trusted")
			}
		}
	})

	// (c) tie-break: winner = branch with lower first-diverging-entry hash
	t.Run("tiebreak", func(t *testing.T) {
		var v map[string]string
		must(t, json.Unmarshal(fc["tiebreak"], &v))
		gh := decodeB64(t, v["genesis_hash"])
		x := decodeB64(t, v["chain_x"])
		y := decodeB64(t, v["chain_y"])
		wantTip := decodeB64(t, v["winner_tip"])

		for _, order := range [][2][]byte{{x, y}, {y, x}} {
			s := trustlog.NewStore(gh)
			must(t, s.Ingest(order[0]))
			must(t, s.Ingest(order[1]))
			if !bytes.Equal(s.Tip(), wantTip) {
				t.Error("tie-break winner must be the same regardless of ingest order")
			}
		}
	})
}

// TestRevokeWithReplacementGoldenVector loads the shared fork_choice.revoke_with_replacement
// section from vectors.json and asserts Go resolves it identically to Dart, pinning Go↔Dart
// parity for KindRevokeSigner with a Replaces field.
func TestRevokeWithReplacementGoldenVector(t *testing.T) {
	raw, err := os.ReadFile("../../app/test/e2e/testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vectors.json: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal vectors.json: %v", err)
	}
	var fc map[string]json.RawMessage
	if err := json.Unmarshal(top["fork_choice"], &fc); err != nil {
		t.Fatalf("unmarshal fork_choice: %v", err)
	}
	var v map[string]string
	if err := json.Unmarshal(fc["revoke_with_replacement"], &v); err != nil {
		t.Fatalf("unmarshal revoke_with_replacement: %v", err)
	}
	gh := decodeB64(t, v["genesis_hash"])
	honest := decodeB64(t, v["honest_chain"])
	cBranch := decodeB64(t, v["c_branch"])
	wantTip := decodeB64(t, v["winner_tip"])
	sigA := decodeB64(t, v["winner_signer_a"])
	sigB := decodeB64(t, v["winner_signer_b"])
	sigD := decodeB64(t, v["winner_signer_d"]) // replacement signer
	sigC := decodeB64(t, v["loser_signer_c"])
	devC := decodeB64(t, v["loser_dev_c"])

	for _, order := range [][2][]byte{{honest, cBranch}, {cBranch, honest}} {
		s := trustlog.NewStore(gh)
		if err := s.Ingest(order[0]); err != nil {
			t.Fatalf("ingest first: %v", err)
		}
		if err := s.Ingest(order[1]); err != nil {
			t.Fatalf("ingest second: %v", err)
		}
		if !bytes.Equal(s.Tip(), wantTip) {
			t.Error("revoke_with_replacement: winner tip mismatch")
		}
		if !s.SignerTrusted(sigA) || !s.SignerTrusted(sigB) {
			t.Error("honest signers a,b must stay trusted")
		}
		if !s.SignerTrusted(sigD) {
			t.Error("replacement signer d must be trusted after revoke-with-replacement")
		}
		if s.SignerTrusted(sigC) {
			t.Error("revoked signer c must not be trusted")
		}
		if s.DeviceAuthorized(devC) {
			t.Error("devC authorized by revoked c must not be authorized")
		}
	}
}

func decodeB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	return b
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

// mustGenSignerSeed generates a deterministic Ed25519 SignerKey from a 32-byte seed
// (byte fill of v), used for reproducible fork-choice vector generation.
func mustGenSignerSeed(t *testing.T, v byte) trustlog.SignerKey {
	t.Helper()
	seed := bytesFill(v)
	priv := ed25519.NewKeyFromSeed(seed)
	return trustlog.SignerKey{Public: priv.Public().(ed25519.PublicKey), Private: priv}
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
