package node

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func TestLoadOrCreateIdentityPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "node-identity.json")
	kp1, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(kp1.Public) != 32 || len(kp1.Private) != 32 {
		t.Fatalf("key sizes = pub %d priv %d", len(kp1.Public), len(kp1.Private))
	}
	kp2, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if string(kp1.Private) != string(kp2.Private) || string(kp1.Public) != string(kp2.Public) {
		t.Error("reload returned a different keypair; identity not persisted")
	}
}

func TestHandleNodeIdentifyIncludesPubKey(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	kp, _ := e2e.GenerateKeyPair()
	d.SetIdentityKey(kp)
	res, err := d.handleNodeIdentify(context.Background(), nil)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	id := res.(api.IdentifyResult)
	if id.IdentityPubKey != base64.StdEncoding.EncodeToString(kp.Public) {
		t.Errorf("identity_pubkey = %q, want base64 of the set key", id.IdentityPubKey)
	}
}
