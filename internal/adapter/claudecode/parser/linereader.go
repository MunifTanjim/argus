package parser

import (
	"bufio"
	"io"
)

const (
	// initialBufSize is the starting buffer capacity for the line reader.
	initialBufSize = 64 * 1024

	// maxLineSize: lines over this are skipped (not fatal). 64 MiB covers
	// even the largest Claude API responses.
	maxLineSize = 64 * 1024 * 1024
)

// lineReader reads JSONL line by line, skipping over-long lines rather than
// aborting. After iteration, call Err() to check for I/O errors (not EOF).
type lineReader struct {
	r         *bufio.Reader
	maxLen    int // 0 means use maxLineSize constant
	buf       []byte
	err       error
	bytesRead int64
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{
		r:   bufio.NewReaderSize(r, initialBufSize),
		buf: make([]byte, 0, initialBufSize),
	}
}

// next returns the next non-empty line (no trailing newline) and true, or
// ("", false) at EOF or I/O error. Use Err() to distinguish the two.
func (lr *lineReader) next() (string, bool) {
	for {
		line, err := lr.readLine()
		if err != nil {
			if err != io.EOF {
				lr.err = err
			}
			return "", false
		}
		if line != "" {
			return line, true
		}
		// Empty line or skipped oversized line -- continue.
	}
}

// Err returns the first non-EOF I/O error encountered, or nil.
func (lr *lineReader) Err() error {
	return lr.err
}

// BytesRead returns total bytes consumed, including skipped lines and newline
// delimiters.
func (lr *lineReader) BytesRead() int64 {
	return lr.bytesRead
}

// readLine reads a full line, returning "" for blank/oversized lines and a
// non-nil error only at EOF or read failure. On oversize, the buffer is
// dropped but the rest of the line is still consumed to keep bytesRead accurate.
func (lr *lineReader) readLine() (string, error) {
	lr.buf = lr.buf[:0]
	oversized := false

	for {
		chunk, isPrefix, err := lr.r.ReadLine()
		// Count data bytes from every chunk, including oversized lines.
		lr.bytesRead += int64(len(chunk))

		if err != nil {
			if len(lr.buf) > 0 && err == io.EOF {
				break // final line ended at EOF, no \n to count
			}
			return "", err
		}

		// ReadLine strips \n but we consumed it, so add +1 on a complete line.
		// Caveat: ReadLine can't tell "\n-terminated" from "EOF without \n",
		// so BytesRead may overcount by 1 on a final line lacking a trailing
		// newline. Harmless for JSONL tailing: real entries always end with \n.
		if !isPrefix {
			lr.bytesRead++
		}

		if oversized {
			if !isPrefix {
				return "", nil // done skipping
			}
			continue
		}

		lr.buf = append(lr.buf, chunk...)

		limit := maxLineSize
		if lr.maxLen > 0 {
			limit = lr.maxLen
		}
		if len(lr.buf) > limit {
			oversized = true
			lr.buf = lr.buf[:0]
			if !isPrefix {
				return "", nil
			}
			continue
		}

		if !isPrefix {
			break
		}
	}

	return string(lr.buf), nil
}
