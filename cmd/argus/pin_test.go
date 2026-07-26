package main

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/trustpin"
)

func testGenesis(b byte) []byte {
	g := make([]byte, trustpin.GenesisLen)
	for i := range g {
		g[i] = b
	}
	return g
}

func TestResolvePinPrefersConfig(t *testing.T) {
	f := trustpin.New(filepath.Join(t.TempDir(), "trustlog-genesis"))
	cfgG := testGenesis(0x77)

	p, err := trustpin.Resolve(base64.StdEncoding.EncodeToString(cfgG), f)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Source != trustpin.SourceConfig {
		t.Fatalf("Source = %v, want config", p.Source)
	}
}

func TestResolvePinRejectsShortConfigGenesis(t *testing.T) {
	f := trustpin.New(filepath.Join(t.TempDir(), "trustlog-genesis"))
	short := base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})

	_, err := trustpin.Resolve(short, f)
	if err == nil {
		t.Fatal("a 4-byte lock.genesis must be rejected")
	}
	if !strings.Contains(err.Error(), "lock.genesis") {
		t.Fatalf("error must name the config key, got: %v", err)
	}
}

func TestPinFilePathsAreDistinct(t *testing.T) {
	if nodePinFile().Path() == clientPinFile().Path() {
		t.Fatal("node and client pin files must be distinct paths")
	}
	if !strings.HasSuffix(nodePinFile().Path(), "trustlog-genesis") {
		t.Fatalf("node pin path = %q, want it to end in trustlog-genesis", nodePinFile().Path())
	}
	if !strings.HasSuffix(clientPinFile().Path(), "client-trustlog-genesis") {
		t.Fatalf("client pin path = %q, want it to end in client-trustlog-genesis", clientPinFile().Path())
	}
}
