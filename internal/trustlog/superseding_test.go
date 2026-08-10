package trustlog

import (
	"bytes"
	"testing"
)

// chainPair builds a genesis chain, optionally disabled, and returns its bytes and
// genesis hash.
func chainPair(t *testing.T, disable bool, extraDevices int) (chain []byte, genesis []byte) {
	t.Helper()
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	var commitments [][]byte
	var secret []byte
	if disable {
		secret, err = GenerateDisablementSecret()
		if err != nil {
			t.Fatalf("secret: %v", err)
		}
		commitments = [][]byte{DisablementCommitment(secret)}
	}
	log, err := NewGenesis([][]byte{signer.Public}, signer, commitments)
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	genesis = log.Tip()
	for i := 0; i < extraDevices; i++ {
		dev := bytes.Repeat([]byte{byte(0xD0 + i)}, 32)
		if err := log.AuthorizeDevice(dev, signer); err != nil {
			t.Fatalf("authorize: %v", err)
		}
	}
	if disable {
		if err := log.Disable(secret, signer); err != nil {
			t.Fatalf("disable: %v", err)
		}
	}
	return MarshalChain(log.Entries()), genesis
}

// After a relock the gateway still holds the dead root; naming it would send the
// operator to compare a fingerprint that matches nothing.
func TestSupersedingGenesisPrefersALiveRoot(t *testing.T) {
	deadChain, deadGenesis := chainPair(t, true, 2)
	liveChain, liveGenesis := chainPair(t, false, 0)

	got := SupersedingGenesis([][]byte{deadChain, liveChain}, nil)

	if !bytes.Equal(got, liveGenesis) {
		t.Fatalf("picked %x, want the live root %x (dead was %x)", got, liveGenesis, deadGenesis)
	}
}

// Two relocks leave two live successors retained; the longer one is the one the
// network actually advanced.
func TestSupersedingGenesisPrefersTheLongestLiveRoot(t *testing.T) {
	shortChain, _ := chainPair(t, false, 0)
	longChain, longGenesis := chainPair(t, false, 3)

	got := SupersedingGenesis([][]byte{shortChain, longChain}, nil)

	if !bytes.Equal(got, longGenesis) {
		t.Fatalf("picked %x, want the longer root %x", got, longGenesis)
	}
}

func TestSupersedingGenesisSkipsOurOwnRoot(t *testing.T) {
	ownChain, ownGenesis := chainPair(t, true, 1)
	otherChain, otherGenesis := chainPair(t, false, 0)

	got := SupersedingGenesis([][]byte{ownChain, otherChain}, ownGenesis)

	if !bytes.Equal(got, otherGenesis) {
		t.Fatalf("picked %x, want %x", got, otherGenesis)
	}
}

// All dead means there is no successor to name, but the device is still following a
// root nobody else is on; report something rather than nothing.
func TestSupersedingGenesisFallsBackToADeadRoot(t *testing.T) {
	deadChain, deadGenesis := chainPair(t, true, 0)

	got := SupersedingGenesis([][]byte{deadChain}, nil)

	if !bytes.Equal(got, deadGenesis) {
		t.Fatalf("picked %x, want the only root on offer %x", got, deadGenesis)
	}
}

func TestSupersedingGenesisIgnoresGarbage(t *testing.T) {
	if got := SupersedingGenesis([][]byte{[]byte("not a chain")}, nil); got != nil {
		t.Fatalf("garbage must yield no root, got %x", got)
	}
}
