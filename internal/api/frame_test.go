package api

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

func TestReadFrameMixedAndSplit(t *testing.T) {
	var stream []byte
	stream = appendFrame(stream, EncodingJSON, []byte(`{"a":1}`))
	stream = appendFrame(stream, EncodingCBOR, []byte{0xa1, 0x61, 0x62, 0x02}) // {"b":2}
	stream = append(stream, '\n')                                              // empty line, skipped
	stream = appendFrame(stream, EncodingJSON, []byte(`{"c":3}`))
	r := bufio.NewReader(iotest.OneByteReader(bytes.NewReader(stream)))

	want := []struct {
		enc Encoding
		b   []byte
	}{
		{EncodingJSON, []byte(`{"a":1}`)},
		{EncodingCBOR, []byte{0xa1, 0x61, 0x62, 0x02}},
		{EncodingJSON, []byte(`{"c":3}`)},
	}
	for i, w := range want {
		enc, b, err := readFrame(r)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if enc != w.enc || !bytes.Equal(b, w.b) {
			t.Fatalf("frame %d: got (%d, %x), want (%d, %x)", i, enc, b, w.enc, w.b)
		}
	}
	if _, _, err := readFrame(r); err != io.EOF {
		t.Fatalf("got %v, want io.EOF", err)
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	hdr := []byte{0x00, 0x01, 0x00, 0x00, 0x01} // 16 MiB + 1
	_, _, err := readFrame(bufio.NewReader(bytes.NewReader(hdr)))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrameRejectsOversizeLine(t *testing.T) {
	line := bytes.Repeat([]byte("a"), MaxFrameSize+10)
	_, _, err := readFrame(bufio.NewReader(bytes.NewReader(line)))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrameTruncatedCBOR(t *testing.T) {
	stream := appendFrame(nil, EncodingCBOR, []byte{1, 2, 3})
	_, _, err := readFrame(bufio.NewReader(bytes.NewReader(stream[:7])))
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("got %v, want io.ErrUnexpectedEOF", err)
	}
}
