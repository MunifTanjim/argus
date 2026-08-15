package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/keyfmt"
)

func TestRevokeSignerStartRoutesToRPC(t *testing.T) {
	s1 := bytes.Repeat([]byte{0x11}, 32)
	s2 := bytes.Repeat([]byte{0x22}, 32)
	s3 := bytes.Repeat([]byte{0x33}, 32)

	called := false
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockStatus: func(_ context.Context, _ json.RawMessage) (any, error) {
			return api.LockStatusResult{Enabled: true, Signers: [][]byte{s1, s2, s3}}, nil
		},
		api.MethodLockRevokeSignerStart: func(_ context.Context, _ json.RawMessage) (any, error) {
			called = true
			return api.LockRevokeSignerBlobResult{Blob: []byte("startblob")}, nil
		},
	})
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{"--socket", sock, keyfmt.SignerKey.Encode(s1)})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("revoke-signer start: %v", err)
	}
	if !called {
		t.Fatal("lock.revokeSignerStart was not called")
	}
}

func TestRevokeSignerCosignRoutesToRPC(t *testing.T) {
	fakeBlob := base64.StdEncoding.EncodeToString([]byte("cosignblob"))
	called := false
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockRevokeSignerCosign: func(_ context.Context, _ json.RawMessage) (any, error) {
			called = true
			return api.LockRevokeSignerBlobResult{Blob: []byte("cosignedblob")}, nil
		},
	})
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{"--socket", sock, "--cosign", fakeBlob})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("revoke-signer --cosign: %v", err)
	}
	if !called {
		t.Fatal("lock.revokeSignerCosign was not called")
	}
}

func TestRevokeSignerFinishRoutesToRPC(t *testing.T) {
	fakeBlob := base64.StdEncoding.EncodeToString([]byte("finishblob"))
	called := false
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockRevokeSignerFinish: func(_ context.Context, _ json.RawMessage) (any, error) {
			called = true
			return api.LockRevokeSignerFinishResult{Tip: bytes.Repeat([]byte{0xFF}, 32)}, nil
		},
	})
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{"--socket", sock, "--finish", fakeBlob})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("revoke-signer --finish: %v", err)
	}
	if !called {
		t.Fatal("lock.revokeSignerFinish was not called")
	}
}

func TestRevokeSignerCosignFinishMutualExclusion(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("blob"))
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{"--cosign", blob, "--finish", blob})
	if err := cmd.Execute(); err == nil {
		t.Fatal("--cosign and --finish together must be rejected")
	}
}

func TestRevokeSignerCosignRejectsInvalidBase64(t *testing.T) {
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{"--cosign", "not!!valid-base64"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("invalid --cosign blob must fail")
	}
}

func TestRevokeSignerFinishRejectsInvalidBase64(t *testing.T) {
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{"--finish", "not!!valid-base64"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("invalid --finish blob must fail")
	}
}

func TestRevokeSignerCosignRejectsPositionalArgs(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("blob"))
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{"--cosign", blob, keyfmt.SignerKey.Encode(bytes.Repeat([]byte{0x01}, 32))})
	if err := cmd.Execute(); err == nil {
		t.Fatal("--cosign with positional args must be rejected")
	}
}

func TestRevokeSignerFinishRejectsPositionalArgs(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("blob"))
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{"--finish", blob, keyfmt.SignerKey.Encode(bytes.Repeat([]byte{0x01}, 32))})
	if err := cmd.Execute(); err == nil {
		t.Fatal("--finish with positional args must be rejected")
	}
}

// A device key passed as the compromised signer must be refused; only sigpub: is valid.
func TestRevokeSignerRejectsWrongKeyPrefixOnCompromised(t *testing.T) {
	badKey := keyfmt.DeviceKey.Encode(bytes.Repeat([]byte{0x01}, 32))
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{badKey})
	if err := cmd.Execute(); err == nil {
		t.Fatal("a device key passed as compromised signer must be rejected")
	}
}

// A device key passed as --replacement must be refused; only sigpub: is valid.
func TestRevokeSignerRejectsWrongKeyPrefixOnReplacement(t *testing.T) {
	validKey := keyfmt.SignerKey.Encode(bytes.Repeat([]byte{0x01}, 32))
	badReplacement := keyfmt.DeviceKey.Encode(bytes.Repeat([]byte{0x02}, 32))
	cmd := newLockRevokeSignerCmd()
	cmd.SetArgs([]string{validKey, "--replacement", badReplacement})
	if err := cmd.Execute(); err == nil {
		t.Fatal("a device key passed as --replacement must be rejected")
	}
}
