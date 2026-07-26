package trustpin_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/trustpin"
)

func genesis(b byte) []byte {
	g := make([]byte, 32)
	for i := range g {
		g[i] = b
	}
	return g
}

func TestFileRoundTrip(t *testing.T) {
	f := trustpin.New(filepath.Join(t.TempDir(), "sub", "trustlog-genesis"))

	got, err := f.Load()
	if err != nil || got != nil {
		t.Fatalf("absent: got %v, %v; want nil, nil", got, err)
	}

	want := genesis(0xA1)
	if err := f.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err = f.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Load = %x, want %x", got, want)
	}

	if err := f.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got, err := f.Load(); err != nil || got != nil {
		t.Fatalf("after Clear: got %v, %v; want nil, nil", got, err)
	}
	if err := f.Clear(); err != nil {
		t.Fatalf("Clear on absent file must not error: %v", err)
	}
}

func TestSaveRejectsWrongLength(t *testing.T) {
	f := trustpin.New(filepath.Join(t.TempDir(), "trustlog-genesis"))
	if err := f.Save([]byte{1, 2, 3, 4}); err == nil {
		t.Fatal("Save of a 4-byte genesis should error")
	}
}

func TestLoadCorruptFileIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trustlog-genesis")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := trustpin.New(path).Load(); err == nil {
		t.Fatal("a present-but-corrupt pin must error, never silently revert to open mode")
	}
}

func TestDecode(t *testing.T) {
	want := genesis(0x5C)
	got, err := trustpin.Decode(base64.StdEncoding.EncodeToString(want))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Decode = %x, want %x", got, want)
	}
	if _, err := trustpin.Decode("not!base64!"); err == nil {
		t.Fatal("malformed base64 should error")
	}
	if _, err := trustpin.Decode(base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})); err == nil {
		t.Fatal("a 4-byte genesis should error")
	}
}

func TestResolvePrecedence(t *testing.T) {
	cfgG, fileG := genesis(0x11), genesis(0x22)
	cfgB64 := base64.StdEncoding.EncodeToString(cfgG)

	t.Run("neither", func(t *testing.T) {
		p, err := trustpin.Resolve("", trustpin.New(filepath.Join(t.TempDir(), "pin")))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if p.Genesis != nil || p.Source != trustpin.SourceNone {
			t.Fatalf("got %v/%v, want nil/SourceNone", p.Genesis, p.Source)
		}
	})

	t.Run("config only", func(t *testing.T) {
		p, err := trustpin.Resolve(cfgB64, trustpin.New(filepath.Join(t.TempDir(), "pin")))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !bytes.Equal(p.Genesis, cfgG) || p.Source != trustpin.SourceConfig {
			t.Fatalf("got %x/%v, want config pin", p.Genesis, p.Source)
		}
	})

	t.Run("file only", func(t *testing.T) {
		f := trustpin.New(filepath.Join(t.TempDir(), "pin"))
		if err := f.Save(fileG); err != nil {
			t.Fatalf("Save: %v", err)
		}
		p, err := trustpin.Resolve("", f)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !bytes.Equal(p.Genesis, fileG) || p.Source != trustpin.SourceFile {
			t.Fatalf("got %x/%v, want file pin", p.Genesis, p.Source)
		}
	})

	t.Run("both agreeing", func(t *testing.T) {
		f := trustpin.New(filepath.Join(t.TempDir(), "pin"))
		if err := f.Save(cfgG); err != nil {
			t.Fatalf("Save: %v", err)
		}
		p, err := trustpin.Resolve(cfgB64, f)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !bytes.Equal(p.Genesis, cfgG) || p.Source != trustpin.SourceConfig {
			t.Fatalf("got %x/%v, want config pin", p.Genesis, p.Source)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		f := trustpin.New(filepath.Join(t.TempDir(), "pin"))
		if err := f.Save(fileG); err != nil {
			t.Fatalf("Save: %v", err)
		}
		_, err := trustpin.Resolve(cfgB64, f)
		if err == nil {
			t.Fatal("config and file disagreeing must be a hard error")
		}
		if !strings.Contains(err.Error(), "lock unpin") {
			t.Fatalf("conflict error must name the recovery command, got: %v", err)
		}
	})

	t.Run("corrupt file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pin")
		if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := trustpin.Resolve("", trustpin.New(path)); err == nil {
			t.Fatal("corrupt pin file must fail Resolve")
		}
	})
}
