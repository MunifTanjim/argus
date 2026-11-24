package parser

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// asDisplayed returns b as Claude Code shows it: a narration block becomes a
// text block. Empty narration stays thinking, as it does there.
func (b contentBlockJSON) asDisplayed() contentBlockJSON {
	if b.Type == "thinking" && strings.TrimSpace(b.Thinking) != "" && isNarration(b.Signature) {
		return contentBlockJSON{Type: "text", Text: b.Thinking}
	}
	return b
}

// isNarration reports whether a thinking block's signature carries the
// "narration" block kind. The server uses that kind for summaries of the
// prose the model wrote between tool calls. The kind lives at protobuf path
// 2 → 1 → 8 inside the base64 signature. The format is undocumented, so any
// decode failure reports false and the block stays thinking.
func isNarration(signature string) bool {
	b, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	for _, field := range []uint64{2, 1, 8} {
		var ok bool
		if b, ok = protoBytesField(b, field); !ok {
			return false
		}
	}
	return string(b) == "narration"
}

// protoBytesField returns the first length-delimited field with the given
// number from a protobuf message. It reports false for a missing field or a
// malformed message.
func protoBytesField(b []byte, field uint64) ([]byte, bool) {
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, false
		}
		b = b[n:]
		switch key & 7 {
		case 0: // varint
			_, n = binary.Uvarint(b)
			if n <= 0 {
				return nil, false
			}
			b = b[n:]
		case 1: // 64-bit
			if len(b) < 8 {
				return nil, false
			}
			b = b[8:]
		case 2: // length-delimited
			size, n := binary.Uvarint(b)
			if n <= 0 || size > uint64(len(b)-n) {
				return nil, false
			}
			val := b[n : n+int(size)]
			if key>>3 == field {
				return val, true
			}
			b = b[n+int(size):]
		case 5: // 32-bit
			if len(b) < 4 {
				return nil, false
			}
			b = b[4:]
		default:
			return nil, false
		}
	}
	return nil, false
}
