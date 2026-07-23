package trustlog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Decode caps: chains arrive over the untrusted gateway relay, so every
// attacker-controllable length/count is bounded before allocation.
const (
	maxField        = 1 << 20 // 1 MiB per field (keys/sigs/hashes are tiny; generous)
	maxSigners      = 1 << 12 // signers in a genesis
	maxDisablements = 1 << 12 // disablement commitments in a genesis
	maxEntries      = 1 << 16 // entries in a chain
	maxCoSigns      = 1 << 12 // co-signs in a KindRevokeSigner entry
	maxReplaces     = 1 << 12 // replacement signers in a KindRevokeSigner entry
)

// MarshalEntry encodes an entry to its canonical wire form. It is identical to the
// bytes hashEntry covers, so decode→hash reproduces the original chain hash.
// For KindRevokeSigner, sigBytes already covers Replaces, and CoSigns are
// appended after Sig (mirroring hashEntry).
func MarshalEntry(e Entry) []byte {
	var buf bytes.Buffer
	buf.Write(sigBytes(&e))
	putField(&buf, e.Sig)
	if e.Kind == KindRevokeSigner {
		var cnt [4]byte
		binary.BigEndian.PutUint32(cnt[:], uint32(len(e.CoSigns)))
		buf.Write(cnt[:])
		for _, cs := range e.CoSigns {
			putField(&buf, cs.Signer)
			putField(&buf, cs.Sig)
		}
	}
	return buf.Bytes()
}

func getField(r *bytes.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	if n > maxField {
		return nil, fmt.Errorf("trustlog: field length %d exceeds cap", n)
	}
	if n == 0 {
		return nil, nil
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

func getCount(r *bytes.Reader, cap uint32) (uint32, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return 0, err
	}
	if n > cap {
		return 0, fmt.Errorf("trustlog: count %d exceeds cap", n)
	}
	return n, nil
}

// UnmarshalEntry decodes a canonical entry. It rejects truncation, oversized
// prefixes, and trailing bytes.
func UnmarshalEntry(b []byte) (Entry, error) {
	r := bytes.NewReader(b)
	e, err := readEntry(r)
	if err != nil {
		return Entry{}, err
	}
	if r.Len() != 0 {
		return Entry{}, errors.New("trustlog: trailing bytes after entry")
	}
	return e, nil
}

func readEntry(r *bytes.Reader) (Entry, error) {
	kind, err := r.ReadByte()
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	e.Kind = Kind(kind)
	if e.Prev, err = getField(r); err != nil {
		return Entry{}, err
	}
	cnt, err := getCount(r, maxSigners)
	if err != nil {
		return Entry{}, err
	}
	if cnt > 0 {
		e.Signers = make([][]byte, cnt)
		for i := range e.Signers {
			if e.Signers[i], err = getField(r); err != nil {
				return Entry{}, err
			}
		}
	}
	dcnt, err := getCount(r, maxDisablements)
	if err != nil {
		return Entry{}, err
	}
	if dcnt > 0 {
		e.Disablements = make([][]byte, dcnt)
		for i := range e.Disablements {
			if e.Disablements[i], err = getField(r); err != nil {
				return Entry{}, err
			}
		}
	}
	if e.Key, err = getField(r); err != nil {
		return Entry{}, err
	}
	if e.Signer, err = getField(r); err != nil {
		return Entry{}, err
	}
	if e.Kind == KindRevokeSigner {
		rcnt, err := getCount(r, maxReplaces)
		if err != nil {
			return Entry{}, err
		}
		if rcnt > 0 {
			e.Replaces = make([][]byte, rcnt)
			for i := range e.Replaces {
				if e.Replaces[i], err = getField(r); err != nil {
					return Entry{}, err
				}
			}
		}
	}
	if e.Sig, err = getField(r); err != nil {
		return Entry{}, err
	}
	if e.Kind == KindRevokeSigner {
		cscnt, err := getCount(r, maxCoSigns)
		if err != nil {
			return Entry{}, err
		}
		if cscnt > 0 {
			e.CoSigns = make([]CoSign, cscnt)
			for i := range e.CoSigns {
				if e.CoSigns[i].Signer, err = getField(r); err != nil {
					return Entry{}, err
				}
				if e.CoSigns[i].Sig, err = getField(r); err != nil {
					return Entry{}, err
				}
			}
		}
	}
	return e, nil
}

// MarshalChain encodes a sequence of entries: a count then each entry length-prefixed.
func MarshalChain(entries []Entry) []byte {
	var buf bytes.Buffer
	var cnt [4]byte
	binary.BigEndian.PutUint32(cnt[:], uint32(len(entries)))
	buf.Write(cnt[:])
	for i := range entries {
		putField(&buf, MarshalEntry(entries[i]))
	}
	return buf.Bytes()
}

// UnmarshalChain decodes a chain produced by MarshalChain, rejecting garbage,
// truncation, oversized counts/fields, and trailing bytes.
func UnmarshalChain(b []byte) ([]Entry, error) {
	r := bytes.NewReader(b)
	cnt, err := getCount(r, maxEntries)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for i := uint32(0); i < cnt; i++ {
		raw, err := getField(r)
		if err != nil {
			return nil, err
		}
		e, err := UnmarshalEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("trustlog: entry %d: %w", i, err)
		}
		entries = append(entries, e)
	}
	if r.Len() != 0 {
		return nil, errors.New("trustlog: trailing bytes after chain")
	}
	return entries, nil
}
