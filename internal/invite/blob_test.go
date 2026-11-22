package invite

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func testBlob() Blob {
	return Blob{
		GatewayURL: "https://gw.example.com",
		Token:      "tok",
		Genesis:    bytes.Repeat([]byte{1}, 32),
		IssuerID:   "issuer",
		IssuerKey:  bytes.Repeat([]byte{2}, 32),
		InviteID:   bytes.Repeat([]byte{3}, 16),
		Secret:     bytes.Repeat([]byte{4}, 32),
	}
}

func TestBlobRoundTrip(t *testing.T) {
	b := testBlob()
	enc, err := b.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, BlobPrefix) {
		t.Fatalf("missing prefix: %q", enc)
	}
	if strings.ContainsAny(strings.TrimPrefix(enc, BlobPrefix), "=+/") {
		t.Fatalf("not raw base64url: %q", enc)
	}
	got, err := DecodeBlob(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, b) {
		t.Fatalf("got %+v, want %+v", got, b)
	}
}

func TestDecodeBlobTrimsWhitespace(t *testing.T) {
	enc, err := testBlob().Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBlob("  " + enc[:20] + "\n" + enc[20:] + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeBlobRejects(t *testing.T) {
	enc := func(mut func(*Blob)) string {
		b := testBlob()
		mut(&b)
		s, err := b.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	good := enc(func(*Blob) {})
	tests := []struct {
		name, in, want string
	}{
		{"missing prefix", strings.TrimPrefix(good, BlobPrefix), "prefix"},
		{"bad base64", BlobPrefix + "!!!", "base64"},
		{"bad cbor", BlobPrefix + "AAAA", "cbor"},
		{"genesis", enc(func(b *Blob) { b.Genesis = b.Genesis[:31] }), "genesis"},
		{"issuer key", enc(func(b *Blob) { b.IssuerKey = nil }), "issuer key"},
		{"invite id", enc(func(b *Blob) { b.InviteID = append(b.InviteID, 0) }), "invite id"},
		{"secret", enc(func(b *Blob) { b.Secret = b.Secret[:1] }), "secret"},
		{"gateway url", enc(func(b *Blob) { b.GatewayURL = "" }), "gateway url"},
		{"issuer id", enc(func(b *Blob) { b.IssuerID = "" }), "issuer id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeBlob(tt.in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
