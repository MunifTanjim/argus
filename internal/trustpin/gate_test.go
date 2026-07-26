package trustpin_test

import (
	"bytes"
	"sync"
	"testing"

	"github.com/MunifTanjim/argus/internal/trustpin"
)

func TestGateZeroValueIsOpen(t *testing.T) {
	var g trustpin.Gate
	if g.Tripped() {
		t.Fatal("zero-value gate must not be tripped")
	}
	if g.Genesis() != nil {
		t.Fatal("zero-value gate must report no genesis")
	}
}

func TestGateTripIsIdempotentAndKeepsFirstGenesis(t *testing.T) {
	var g trustpin.Gate
	first, second := genesis(0x01), genesis(0x02)

	g.Trip(first)
	if !g.Tripped() {
		t.Fatal("gate should be tripped")
	}
	g.Trip(second)
	if !bytes.Equal(g.Genesis(), first) {
		t.Fatalf("Genesis = %x, want the first observed %x", g.Genesis(), first)
	}
}

func TestGateGenesisIsACopy(t *testing.T) {
	var g trustpin.Gate
	in := genesis(0x33)
	g.Trip(in)
	out := g.Genesis()
	out[0] ^= 0xFF
	if !bytes.Equal(g.Genesis(), in) {
		t.Fatal("Genesis must return a copy the caller cannot mutate")
	}
}

func TestGateClear(t *testing.T) {
	var g trustpin.Gate
	g.Trip(genesis(0x44))
	g.Clear()
	if g.Tripped() || g.Genesis() != nil {
		t.Fatal("Clear must release the gate and drop the genesis")
	}
	g.Trip(genesis(0x55))
	if !g.Tripped() {
		t.Fatal("gate must be re-trippable after Clear")
	}
}

func TestGateConcurrentTrip(t *testing.T) {
	var g trustpin.Gate
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(b byte) {
			defer wg.Done()
			g.Trip(genesis(b))
			_ = g.Tripped()
			_ = g.Genesis()
		}(byte(i))
	}
	wg.Wait()
	if !g.Tripped() || len(g.Genesis()) != trustpin.GenesisLen {
		t.Fatal("concurrent trips must settle on exactly one genesis")
	}
}

// TestGateTripWithoutAGenesisStillFailsClosed pins the gate's invariant: a trip is
// a trip even when the caller had no genesis to record, and a tripped gate never
// reports a nil genesis (which a caller could read as "open").
func TestGateTripWithoutAGenesisStillFailsClosed(t *testing.T) {
	var g trustpin.Gate
	g.Trip(nil)
	if !g.Tripped() {
		t.Fatal("Trip(nil) must still fail the device closed")
	}
	if g.Genesis() == nil {
		t.Fatal("a tripped gate must never report a nil genesis")
	}
	if len(g.Genesis()) != 0 {
		t.Fatalf("Genesis = %x, want empty", g.Genesis())
	}
}

// TestGateClearThenTripAdoptsTheNewGenesis exercises re-pinning: the genesis
// recorded before a Clear must not survive it. Concurrent readers run throughout so
// -race covers the reader path, but the assertion is on the writer sequence, not on
// a self-consistency property the type derives for free.
func TestGateClearThenTripAdoptsTheNewGenesis(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		var g trustpin.Gate
		old, fresh := genesis(0xAA), genesis(0xBB)
		g.Trip(old)

		stop := make(chan struct{})
		var readers sync.WaitGroup
		for r := 0; r < 2; r++ {
			readers.Add(1)
			go func() {
				defer readers.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					if gen := g.Genesis(); gen != nil && !bytes.Equal(gen, old) && !bytes.Equal(gen, fresh) {
						t.Errorf("iter %d: reader saw a genesis that was never tripped: %x", iter, gen)
						return
					}
				}
			}()
		}

		g.Clear()
		g.Trip(fresh)
		close(stop)
		readers.Wait()

		if !g.Tripped() {
			t.Fatalf("iter %d: gate must be tripped after Clear+Trip", iter)
		}
		if !bytes.Equal(g.Genesis(), fresh) {
			t.Fatalf("iter %d: Genesis = %x, want the post-Clear genesis %x", iter, g.Genesis(), fresh)
		}
	}
}
