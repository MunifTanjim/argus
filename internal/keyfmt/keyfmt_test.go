package keyfmt_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/keyfmt"
)

func val(b byte) []byte {
	v := make([]byte, keyfmt.Len)
	for i := range v {
		v[i] = b
	}
	return v
}

func TestRoundTrip(t *testing.T) {
	for _, k := range []keyfmt.Kind{keyfmt.SignerKey, keyfmt.DeviceKey, keyfmt.Genesis, keyfmt.Disablement} {
		want := val(0x9f)
		s := k.Encode(want)
		if !strings.HasPrefix(s, k.Prefix()) {
			t.Fatalf("%s: Encode = %q, want prefix %q", k.Label(), s, k.Prefix())
		}
		if got := len(s) - len(k.Prefix()); got != keyfmt.Len*2 {
			t.Fatalf("%s: body is %d chars, want %d", k.Label(), got, keyfmt.Len*2)
		}
		if strings.ToLower(s) != s {
			t.Fatalf("%s: Encode = %q, want lowercase", k.Label(), s)
		}
		got, err := k.Decode(s)
		if err != nil {
			t.Fatalf("%s: Decode: %v", k.Label(), err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: Decode = %x, want %x", k.Label(), got, want)
		}
	}
}

// The point of the package: a value of one kind must not parse as another, even
// though every kind is the same 32 bytes.
func TestWrongKindIsRejectedByName(t *testing.T) {
	genesis := keyfmt.Genesis.Encode(val(0x11))

	_, err := keyfmt.DeviceKey.Decode(genesis)
	if err == nil {
		t.Fatal("a genesis hash must not decode as a device key")
	}
	for _, want := range []string{"device key", "devpub:", "genesis hash", "gen:"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must name %q", err, want)
		}
	}
}

func TestUntaggedIsRejectedWithTheExpectedForm(t *testing.T) {
	// A bare base64 pubkey, the old spelling.
	_, err := keyfmt.SignerKey.Decode("j8K2fQrs0YHhP1kZ8m3vQb2dLcW9xTn4pR7sVu6yAaI=")
	if err == nil {
		t.Fatal("an untagged value must not decode")
	}
	if !strings.Contains(err.Error(), "sigpub:") {
		t.Fatalf("error %q must show the expected form", err)
	}
}

func TestDecodeRejectsBadHexAndWrongLength(t *testing.T) {
	if _, err := keyfmt.Genesis.Decode("gen:zzzz"); err == nil {
		t.Fatal("non-hex body must error")
	}
	if _, err := keyfmt.Genesis.Decode("gen:1a2b3c"); err == nil {
		t.Fatal("a 3-byte body must error")
	}
	if _, err := keyfmt.Genesis.Decode("gen:1a2b3c"); err != nil && !strings.Contains(err.Error(), "want 32") {
		t.Fatalf("length error %q must state the expected length", err)
	}
}

func TestSurroundingWhitespaceIsTolerated(t *testing.T) {
	want := val(0x42)
	got, err := keyfmt.Genesis.Decode("  " + keyfmt.Genesis.Encode(want) + "\n")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("whitespace-padded value must decode")
	}
}

func TestTaggedDistinguishesKeysFromNodeNames(t *testing.T) {
	if keyfmt.Tagged("node-b") {
		t.Fatal("a node name must not look like a key")
	}
	if !keyfmt.Tagged(keyfmt.SignerKey.Encode(val(1))) {
		t.Fatal("a signer key must be recognised as tagged")
	}
	if !keyfmt.Tagged(keyfmt.Disablement.Encode(val(2))) {
		t.Fatal("a disablement secret must be recognised as tagged")
	}
}
