package keyfile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeKey is a minimal key type for exercising the generic LoadOrCreate flow.
type fakeKey struct{ priv, pub []byte }

func fakeGen() (fakeKey, error) {
	return fakeKey{priv: bytes.Repeat([]byte{1}, 4), pub: bytes.Repeat([]byte{2}, 3)}, nil
}
func fakeSplit(k fakeKey) (priv, pub []byte) { return k.priv, k.pub }
func fakeBuild(priv, pub []byte) (fakeKey, bool) {
	if len(priv) != 4 || len(pub) != 3 {
		return fakeKey{}, false
	}
	return fakeKey{priv: priv, pub: pub}, true
}

func load(t *testing.T, path string) (fakeKey, error) {
	t.Helper()
	return LoadOrCreate(path, "test", fakeGen, fakeSplit, fakeBuild)
}

// TestLoadOrCreateGeneratesWhenAbsent: a missing file creates a fresh key (0600)
// and persists it so the next load returns the identical key.
func TestLoadOrCreateGeneratesWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.json")
	k1, err := load(t, path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms = %v, want 0600", fi.Mode().Perm())
	}
	k2, err := load(t, path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !bytes.Equal(k1.priv, k2.priv) || !bytes.Equal(k1.pub, k2.pub) {
		t.Fatal("second load returned a different key")
	}
}

// TestLoadOrCreateFailsLoudOnCorruptFile: a present-but-unparseable file must
// return an error and must NOT be silently overwritten with a fresh key —
// silently rotating a trust-anchor key looks like MITM / changes the trusted signer.
func TestLoadOrCreateFailsLoudOnCorruptFile(t *testing.T) {
	cases := map[string][]byte{
		"not json":     []byte("this is not json"),
		"bad base64":   mustJSON(t, persisted{Private: "!!!not-base64!!!", Public: "!!!"}),
		"wrong length": mustJSON(t, persisted{Private: base64.StdEncoding.EncodeToString([]byte{9}), Public: base64.StdEncoding.EncodeToString([]byte{9})}),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key.json")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := load(t, path)
			if err == nil {
				t.Fatal("expected an error for a present-but-corrupt key file, got nil")
			}
			got, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("read back: %v", rerr)
			}
			if !bytes.Equal(got, content) {
				t.Fatal("corrupt key file must not be overwritten with a fresh key")
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
