package e2e

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func newSessionPair(t *testing.T) (client, node *Session) {
	t.Helper()
	nodeKey, _ := GenerateKeyPair()
	clientKey, _ := GenerateKeyPair()
	prologue := []byte("argus-e2e/v1|test")
	init, msg1, err := NewInitiator(clientKey, nodeKey.Public, prologue)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	node, _, msg2, err := Respond(nodeKey, prologue, msg1)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	client, err = init.Finish(msg2)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return client, node
}

func TestGenerateKeyPairProducesDistinct32ByteKeys(t *testing.T) {
	a, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if len(a.Private) != 32 || len(a.Public) != 32 {
		t.Fatalf("key sizes = priv %d pub %d, want 32/32", len(a.Private), len(a.Public))
	}
	b, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if string(a.Private) == string(b.Private) {
		t.Error("two keypairs share a private key")
	}
}

func TestHandshakeRoundTripSealsBothDirections(t *testing.T) {
	nodeKey, _ := GenerateKeyPair()   // responder (node) static key
	clientKey, _ := GenerateKeyPair() // initiator (client) static key
	prologue := []byte("argus-e2e/v1|node-1|chan-7")

	init, msg1, err := NewInitiator(clientKey, nodeKey.Public, prologue)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	nodeSess, _, msg2, err := Respond(nodeKey, prologue, msg1)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	clientSess, err := init.Finish(msg2)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// client -> node
	sealed, err := clientSess.Seal([]byte("hello node"))
	if err != nil {
		t.Fatalf("client Seal: %v", err)
	}
	got, err := nodeSess.Open(sealed)
	if err != nil {
		t.Fatalf("node Open: %v", err)
	}
	if string(got) != "hello node" {
		t.Errorf("node opened %q, want %q", got, "hello node")
	}

	// node -> client
	sealed, err = nodeSess.Seal([]byte("hello client"))
	if err != nil {
		t.Fatalf("node Seal: %v", err)
	}
	got, err = clientSess.Open(sealed)
	if err != nil {
		t.Fatalf("client Open: %v", err)
	}
	if string(got) != "hello client" {
		t.Errorf("client opened %q, want %q", got, "hello client")
	}
}

func TestSealOpenLargeMessageChunks(t *testing.T) {
	client, node := newSessionPair(t)

	// 200 KiB forces multiple Noise records (each <= 65535 bytes ciphertext).
	payload := bytes.Repeat([]byte("argus"), 40*1024) // 200 KiB
	sealed, err := client.Seal(payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(sealed) <= maxChunk {
		t.Fatalf("expected a multi-record blob, got %d bytes", len(sealed))
	}
	got, err := node.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("round-trip mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestSealOpenEmptyMessage(t *testing.T) {
	client, node := newSessionPair(t)
	sealed, err := client.Seal(nil)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := node.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty round-trip returned %d bytes", len(got))
	}
}

func TestWrongNodeKeyFailsHandshake(t *testing.T) {
	realNode, _ := GenerateKeyPair()
	imposter, _ := GenerateKeyPair() // a different node key (e.g. a MITM's)
	clientKey, _ := GenerateKeyPair()
	prologue := []byte("argus-e2e/v1|node-1")

	// Client believes it is talking to realNode, but the responder holds imposter's key.
	init, msg1, err := NewInitiator(clientKey, realNode.Public, prologue)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	if _, _, _, err := Respond(imposter, prologue, msg1); err == nil {
		t.Fatal("responder with the wrong static key must fail the IK handshake")
	}
	_ = init
}

func TestPrologueMismatchFailsHandshake(t *testing.T) {
	nodeKey, _ := GenerateKeyPair()
	clientKey, _ := GenerateKeyPair()

	init, msg1, err := NewInitiator(clientKey, nodeKey.Public, []byte("chan-A"))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	// Responder binds a different prologue (e.g. a relayed handshake replayed onto
	// another channel) -> must not establish a session.
	if _, _, _, err := Respond(nodeKey, []byte("chan-B"), msg1); err == nil {
		t.Fatal("prologue mismatch must fail the handshake")
	}
	_ = init
}

func TestTamperedCiphertextFailsOpen(t *testing.T) {
	client, node := newSessionPair(t)
	sealed, err := client.Seal([]byte("transfer $10 to alice"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Flip a bit in the ciphertext body (skip the 2-byte length header).
	sealed[len(sealed)-1] ^= 0x01
	if _, err := node.Open(sealed); err == nil {
		t.Fatal("tampered ciphertext must fail authentication on Open")
	}
}

func TestOpenRejectsEmptyBlob(t *testing.T) {
	_, node := newSessionPair(t)
	if _, err := node.Open(nil); err == nil {
		t.Fatal("Open(nil) must fail: a sealed message is never empty")
	}
}

func TestOpenRejectsTruncatedFinalRecord(t *testing.T) {
	client, node := newSessionPair(t)
	payload := bytes.Repeat([]byte("x"), 200*1024) // forces multiple records
	sealed, err := client.Seal(payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Keep only the first record, dropping the trailing (final) record(s).
	n := int(binary.BigEndian.Uint16(sealed[:2]))
	truncated := sealed[:2+n]
	if _, err := node.Open(truncated); err == nil {
		t.Fatal("Open must reject a blob whose trailing records were dropped")
	}
}

func TestRespondReturnsInitiatorStatic(t *testing.T) {
	nodeKP, _ := GenerateKeyPair()
	clientKP, _ := GenerateKeyPair()
	prologue := []byte("argus-test")

	init, msg1, err := NewInitiator(clientKP, nodeKP.Public, prologue)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	sess, clientStatic, msg2, err := Respond(nodeKP, prologue, msg1)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if !bytes.Equal(clientStatic, clientKP.Public) {
		t.Fatalf("Respond client static = %x, want %x", clientStatic, clientKP.Public)
	}
	if _, err := init.Finish(msg2); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	_ = sess
}

func TestLoadOrCreateIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client-identity.json")
	kp1, err := LoadOrCreateIdentity(path)
	if err != nil || len(kp1.Public) != 32 || len(kp1.Private) != 32 {
		t.Fatalf("first: kp=%v err=%v", kp1, err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms = %v, want 0600", fi.Mode().Perm())
	}
	kp2, err := LoadOrCreateIdentity(path)
	if err != nil || !bytes.Equal(kp1.Public, kp2.Public) || !bytes.Equal(kp1.Private, kp2.Private) {
		t.Fatal("second load returned a different key")
	}
}

func TestLoadOrCreateIdentityUnreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	path := filepath.Join(t.TempDir(), "client-identity.json")
	if err := os.WriteFile(path, []byte("{}"), 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}
	defer os.Chmod(path, 0o600)
	_, err := LoadOrCreateIdentity(path)
	if err == nil {
		t.Fatal("expected an error for an unreadable key file, got nil")
	}
}

func TestFinishRejectsTamperedMsg2(t *testing.T) {
	nodeKey, _ := GenerateKeyPair()
	clientKey, _ := GenerateKeyPair()
	prologue := []byte("argus-e2e/v1|node-1")
	init, msg1, err := NewInitiator(clientKey, nodeKey.Public, prologue)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	_, _, msg2, err := Respond(nodeKey, prologue, msg1)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	msg2[len(msg2)-1] ^= 0x01 // tamper the responder's reply
	if _, err := init.Finish(msg2); err == nil {
		t.Fatal("Finish must reject a tampered msg2 (initiator authenticates the responder)")
	}
}
