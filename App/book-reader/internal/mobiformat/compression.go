// SPDX-License-Identifier: GPL-3.0-only
// PalmDOC/HUFF/CDIC algorithms adapted from KindleUnpack (GPLv3),
// lib/mobi_uncompress.py, revision bf0ca6e. Original copyright holders
// are listed in extract.go. Bounds and resource budgets are enforced here.
package mobiformat

import "encoding/binary"

func palmDOC(in []byte) ([]byte, error) {
	out := make([]byte, 0, 4096)
	for i := 0; i < len(in); {
		c := in[i]
		i++
		switch {
		case c >= 192:
			if len(out)+2 > maxTextRecord {
				return nil, ErrLimit
			}
			out = append(out, ' ', c^128)
		case c >= 128:
			if i >= len(in) {
				return nil, ErrFormat
			}
			next := in[i]
			i++
			distance := int((uint16(c)<<8 | uint16(next)) >> 3 & 0x7ff)
			length := int(next&7) + 3
			if distance == 0 || distance > len(out) {
				return nil, ErrFormat
			}
			if len(out)+length > maxTextRecord {
				return nil, ErrLimit
			}
			for j := 0; j < length; j++ {
				out = append(out, out[len(out)-distance])
			}
		case c >= 1 && c <= 8:
			n := int(c)
			if n > len(in)-i {
				return nil, ErrFormat
			}
			if len(out)+n > maxTextRecord {
				return nil, ErrLimit
			}
			out = append(out, in[i:i+n]...)
			i += n
		default:
			if len(out) == maxTextRecord {
				return nil, ErrLimit
			}
			out = append(out, c)
		}
	}
	return out, nil
}

type huffEntry struct {
	length   uint8
	terminal bool
	max      uint32
}
type huffPhrase struct {
	data    []byte
	literal bool
}
type huffman struct {
	table    [256]huffEntry
	min, max [33]uint32
	phrases  []huffPhrase
	active   []bool
}

func (db *database) loadHuffman(h header) (*huffman, error) {
	b, err := db.record(h.huffStart)
	if err != nil {
		return nil, err
	}
	if len(b) < 24 || string(b[:4]) != "HUFF" || binary.BigEndian.Uint32(b[4:]) != 24 {
		return nil, ErrFormat
	}
	off1, off2 := int64(binary.BigEndian.Uint32(b[8:])), int64(binary.BigEndian.Uint32(b[12:]))
	if off1 < 24 || off2 < 24 || off1+1024 > int64(len(b)) || off2+256 > int64(len(b)) {
		return nil, ErrFormat
	}
	out := &huffman{}
	for i := range out.table {
		v := binary.BigEndian.Uint32(b[off1+int64(i*4):])
		length := uint8(v & 31)
		terminal := v&128 != 0
		if length == 0 || (length <= 8 && !terminal) {
			return nil, ErrFormat
		}
		max := ((uint64(v>>8) + 1) << uint(32-length)) - 1
		if max > 0xffffffff {
			return nil, ErrFormat
		}
		out.table[i] = huffEntry{length, terminal, uint32(max)}
	}
	for i := 1; i <= 32; i++ {
		pos := off2 + int64((i-1)*8)
		lo, hi := binary.BigEndian.Uint32(b[pos:]), binary.BigEndian.Uint32(b[pos+4:])
		if uint64(lo)<<uint(32-i) > 0xffffffff || ((uint64(hi)+1)<<uint(32-i))-1 > 0xffffffff {
			return nil, ErrFormat
		}
		out.min[i] = lo << uint(32-i)
		out.max[i] = uint32(((uint64(hi) + 1) << uint(32-i)) - 1)
	}
	var totalPhrases uint32
	var budget int64
	for i := 1; i < h.huffCount; i++ {
		b, err = db.record(h.huffStart + i)
		if err != nil {
			return nil, err
		}
		budget += int64(len(b))
		if budget > 4<<20 {
			return nil, ErrLimit
		}
		if len(b) < 16 || string(b[:4]) != "CDIC" || binary.BigEndian.Uint32(b[4:]) != 16 {
			return nil, ErrFormat
		}
		count, bits := binary.BigEndian.Uint32(b[8:]), binary.BigEndian.Uint32(b[12:])
		if count == 0 || count > 65536 || bits > 16 {
			return nil, ErrLimit
		}
		if i == 1 {
			totalPhrases = count
			out.phrases = make([]huffPhrase, 0, int(count))
		} else if count != totalPhrases {
			return nil, ErrFormat
		}
		n := min(uint32(1)<<bits, count-uint32(len(out.phrases)))
		if 16+int(n)*2 > len(b) {
			return nil, ErrFormat
		}
		for j := 0; j < int(n); j++ {
			offset := 16 + int(binary.BigEndian.Uint16(b[16+j*2:]))
			if offset < 16+int(n)*2 || offset+2 > len(b) {
				return nil, ErrFormat
			}
			size := binary.BigEndian.Uint16(b[offset:])
			end := offset + 2 + int(size&0x7fff)
			if end > len(b) {
				return nil, ErrFormat
			}
			out.phrases = append(out.phrases, huffPhrase{b[offset+2 : end], size&0x8000 != 0})
		}
	}
	if len(out.phrases) != int(totalPhrases) {
		return nil, ErrFormat
	}
	return out, nil
}

func (h *huffman) unpack(in []byte) ([]byte, error) {
	out := make([]byte, 0, 4096)
	if len(h.active) != len(h.phrases) {
		h.active = make([]bool, len(h.phrases))
	}
	steps := 0
	if err := h.expand(in, &out, h.active, 0, &steps); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *huffman) expand(in []byte, out *[]byte, active []bool, depth int, steps *int) error {
	if depth > 64 {
		return ErrLimit
	}
	for bit := 0; bit < len(in)*8; {
		*steps++
		if *steps > 1<<20 {
			return ErrLimit
		}
		offset, shift := bit/8, uint(bit%8)
		var word uint64
		for i := 0; i < 5; i++ {
			word <<= 8
			if offset+i < len(in) {
				word |= uint64(in[offset+i])
			}
		}
		code := uint32(word >> uint(8-shift))
		e := h.table[code>>24]
		length, maxcode := int(e.length), e.max
		if length == 0 || length > 32 {
			return ErrFormat
		}
		if !e.terminal {
			for length <= 32 && code < h.min[length] {
				length++
			}
			if length > 32 {
				return ErrFormat
			}
			maxcode = h.max[length]
		}
		if bit+length > len(in)*8 {
			break
		}
		bit += length
		if code > maxcode {
			return ErrFormat
		}
		indexValue := (maxcode - code) >> uint(32-length)
		if uint64(indexValue) >= uint64(len(h.phrases)) {
			return ErrFormat
		}
		index := int(indexValue)
		phrase := h.phrases[index]
		if phrase.literal {
			if len(*out)+len(phrase.data) > maxTextRecord {
				return ErrLimit
			}
			*out = append(*out, phrase.data...)
		} else {
			if active[index] {
				return ErrFormat
			}
			active[index] = true
			err := h.expand(phrase.data, out, active, depth+1, steps)
			active[index] = false
			if err != nil {
				return err
			}
		}
	}
	return nil
}
