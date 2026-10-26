package api

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// Encoding identifies how a frame's payload is encoded on the wire.
type Encoding uint8

const (
	EncodingJSON Encoding = iota // one JSON object per line
	EncodingCBOR                 // 0x00, u32 big-endian length, CBOR envelope
)

// MaxFrameSize bounds a single frame's payload.
const MaxFrameSize = 16 << 20

var ErrFrameTooLarge = errors.New("api: frame exceeds 16 MiB")

const cborMarker = 0x00

// readFrame reads the next frame, skipping empty lines. A JSON frame is
// returned without its newline; a CBOR frame without marker and length.
func readFrame(r *bufio.Reader) (Encoding, []byte, error) {
	for {
		first, err := r.Peek(1)
		if err != nil {
			return 0, nil, err
		}
		if first[0] == cborMarker {
			var hdr [5]byte
			if _, err := io.ReadFull(r, hdr[:]); err != nil {
				return 0, nil, unexpectedEOF(err)
			}
			n := binary.BigEndian.Uint32(hdr[1:])
			if n > MaxFrameSize {
				return 0, nil, ErrFrameTooLarge
			}
			payload := make([]byte, n)
			if _, err := io.ReadFull(r, payload); err != nil {
				return 0, nil, unexpectedEOF(err)
			}
			return EncodingCBOR, payload, nil
		}
		line, err := readLine(r)
		if err != nil {
			return 0, nil, err
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			continue
		}
		return EncodingJSON, line, nil
	}
}

// readLine reads through the next '\n', failing once the line exceeds
// MaxFrameSize. A final line without a newline is returned with io.EOF
// suppressed.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > MaxFrameSize+2 {
			return nil, ErrFrameTooLarge
		}
		switch err {
		case nil:
			return line, nil
		case bufio.ErrBufferFull:
		case io.EOF:
			if len(line) > 0 {
				return line, nil
			}
			return nil, err
		default:
			return nil, err
		}
	}
}

func unexpectedEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// appendFrame appends payload to dst framed for enc.
func appendFrame(dst []byte, enc Encoding, payload []byte) []byte {
	if enc == EncodingCBOR {
		dst = append(dst, cborMarker)
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(payload)))
		return append(dst, payload...)
	}
	dst = append(dst, payload...)
	return append(dst, '\n')
}

// writeFrame writes payload framed for enc to w and flushes it, without
// copying payload into an intermediate frame.
func writeFrame(w *bufio.Writer, enc Encoding, payload []byte) error {
	// bufio.Writer errors are sticky; Flush reports any earlier failure.
	if enc == EncodingCBOR {
		var hdr [5]byte
		hdr[0] = cborMarker
		binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
		_, _ = w.Write(hdr[:])
		_, _ = w.Write(payload)
	} else {
		_, _ = w.Write(payload)
		_ = w.WriteByte('\n')
	}
	return w.Flush()
}
