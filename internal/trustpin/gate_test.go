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

func TestGateConcurrentTripAndClear(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		var g trustpin.Gate
		var wg sync.WaitGroup

		wg.Add(4)

		go func() {
			defer wg.Done()
			g.Trip(genesis(0xAA))
		}()

		go func() {
			defer wg.Done()
			g.Clear()
		}()

		for r := 0; r < 2; r++ {
			go func() {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					_ = g.Tripped()
					_ = g.Genesis()
				}
			}()
		}

		wg.Wait()

		tripped := g.Tripped()
		gen := g.Genesis()
		if (tripped && gen == nil) || (!tripped && gen != nil) {
			t.Fatalf("iter %d: final state inconsistent: Tripped=%v, Genesis=%v", iter, tripped, gen != nil)
		}
	}
}
