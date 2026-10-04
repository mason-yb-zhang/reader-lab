// SPDX-License-Identifier: GPL-3.0-only
// Format algorithms adapted from KindleUnpack (GPLv3), revision bf0ca6e:
// Copyright 2009 Charles M. Hannum; extensions 2009-2020 P. Durrant,
// K. Hendricks, S. Siebert, fandrieu, DiapDealer, nickredding, tkeo.
// References: lib/mobi_sectioner.py, mobi_header.py, kindleunpack.py.
package mobiformat

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	maxSource     = 512 << 20
	maxText       = 256 << 20
	maxRecord     = 4 << 20
	maxTextRecord = 65536
	maxIndexBytes = 4 << 20
	maxEntries    = 20000
	absent        = uint32(0xffffffff)
)

var (
	ErrFormat      = errors.New("invalid MOBI format")
	ErrUnsupported = errors.New("unsupported MOBI format")
	ErrDRM         = errors.New("DRM-protected MOBI is unsupported")
	ErrLimit       = errors.New("MOBI resource limit exceeded")
)

// Extract emits reassembled HTML documents in reading order. The callback must
// consume each reader synchronously; encoding is 65001 (UTF-8) or 1252.
// Title is always UTF-8. The caller owns source and tempDir; only a private
// temporary file created by Extract is removed. No image/font resources load.
// KF8 links and styling are not rendered: this interface is for body text.
func Extract(source io.ReaderAt, size int64, tempDir string, visit func(title string, encoding uint32, html io.Reader) error) error {
	if source == nil || visit == nil {
		return ErrFormat
	}
	db, err := openDatabase(source, size)
	if err != nil {
		return err
	}
	h, err := db.header(0)
	if err != nil {
		return err
	}
	if h.version != 8 {
		for i := 1; i+1 < len(db.offsets)-1; i++ {
			if db.offsets[i+1]-db.offsets[i] != 8 {
				continue
			}
			rec, e := db.record(i)
			if e != nil {
				return e
			}
			if string(rec) == "BOUNDARY" {
				h, err = db.header(i + 1)
				if err != nil {
					return err
				}
				if h.version != 8 {
					return fmt.Errorf("%w: KF8 boundary header", ErrFormat)
				}
				break
			}
		}
	}
	var huff *huffman
	if h.compression == 0x4448 {
		huff, err = db.loadHuffman(h)
		if err != nil {
			return err
		}
	}
	raw, err := os.CreateTemp(tempDir, "mobi-raw-*")
	if err != nil {
		return err
	}
	defer os.Remove(raw.Name())
	defer raw.Close()
	var total int64
	for i := 1; i <= h.records; i++ {
		rec, e := db.record(h.start + i)
		if e != nil {
			return e
		}
		rec, e = trimRecord(rec, h.extra)
		if e != nil {
			return e
		}
		var out []byte
		switch h.compression {
		case 1:
			if len(rec) > maxTextRecord {
				return ErrLimit
			}
			out = rec
		case 2:
			out, e = palmDOC(rec)
		case 0x4448:
			out, e = huff.unpack(rec)
		}
		if e != nil {
			return fmt.Errorf("text record %d: %w", i, e)
		}
		if i == 1 && bytes.HasPrefix(out, []byte("%MOP")) {
			return fmt.Errorf("%w: Print Replica", ErrUnsupported)
		}
		total += int64(len(out))
		if total > maxText {
			return ErrLimit
		}
		if _, e = raw.Write(out); e != nil {
			return e
		}
	}
	if total == 0 || (h.textLength != 0 && total != int64(h.textLength)) {
		return fmt.Errorf("%w: text length", ErrFormat)
	}
	if h.version == 8 {
		return db.extractKF8(raw, total, h, visit)
	}
	if h.palm {
		return visit(h.title, h.encoding, io.MultiReader(strings.NewReader("<html><body><pre>"), &escapeReader{r: io.NewSectionReader(raw, 0, total)}, strings.NewReader("</pre></body></html>")))
	}
	return visit(h.title, h.encoding, io.NewSectionReader(raw, 0, total))
}

type database struct {
	source       io.ReaderAt
	offsets      []int64
	name         string
	palm         bool
	indexBytes   int64
	indexEntries int
}

func openDatabase(source io.ReaderAt, size int64) (*database, error) {
	if size < 78 {
		return nil, ErrFormat
	}
	if size > maxSource {
		return nil, ErrLimit
	}
	fixed := make([]byte, 78)
	if _, err := source.ReadAt(fixed, 0); err != nil {
		return nil, err
	}
	signature := string(fixed[60:68])
	if signature != "BOOKMOBI" && signature != "TEXtREAd" {
		return nil, fmt.Errorf("%w: not a MOBI/PalmDOC database", ErrUnsupported)
	}
	n := int(binary.BigEndian.Uint16(fixed[76:78]))
	if n < 2 || int64(78+n*8) > size {
		return nil, ErrFormat
	}
	table := make([]byte, n*8)
	if _, err := source.ReadAt(table, 78); err != nil {
		return nil, err
	}
	db := &database{source: source, offsets: make([]int64, n+1), name: decodeTitle(bytes.TrimRight(fixed[:32], "\x00"), 1252), palm: signature == "TEXtREAd"}
	for i := 0; i < n; i++ {
		offset := int64(binary.BigEndian.Uint32(table[i*8:]))
		if offset < int64(78+n*8) || offset > size || (i > 0 && offset < db.offsets[i-1]) {
			return nil, ErrFormat
		}
		db.offsets[i] = offset
	}
	db.offsets[n] = size
	return db, nil
}

func (db *database) record(n int) ([]byte, error) {
	if n < 0 || n+1 >= len(db.offsets) {
		return nil, ErrFormat
	}
	length := db.offsets[n+1] - db.offsets[n]
	if length > maxRecord {
		return nil, ErrLimit
	}
	b := make([]byte, int(length))
	if len(b) > 0 {
		if _, err := db.source.ReadAt(b, db.offsets[n]); err != nil {
			return nil, err
		}
	}
	return b, nil
}

type header struct {
	start, records                int
	compression                   uint16
	encoding, version, textLength uint32
	title                         string
	extra                         uint16
	huffStart, huffCount          int
	skel, frag, fdst, ncx         uint32
	palm                          bool
}

func (db *database) header(start int) (header, error) {
	h := header{start: start, encoding: 1252, title: db.name, skel: absent, frag: absent, fdst: absent, ncx: absent, palm: db.palm}
	b, err := db.record(start)
	if err != nil {
		return h, err
	}
	if len(b) < 16 {
		return h, ErrFormat
	}
	h.compression = binary.BigEndian.Uint16(b)
	h.textLength = binary.BigEndian.Uint32(b[4:])
	h.records = int(binary.BigEndian.Uint16(b[8:]))
	if h.records < 1 || h.records >= len(db.offsets)-start-1 {
		return h, ErrFormat
	}
	if h.textLength > maxText {
		return h, ErrLimit
	}
	if binary.BigEndian.Uint16(b[12:]) != 0 {
		return h, ErrDRM
	}
	if h.compression != 1 && h.compression != 2 && h.compression != 0x4448 {
		return h, ErrUnsupported
	}
	if h.palm {
		if h.compression == 0x4448 {
			return h, ErrUnsupported
		}
		return h, nil
	}
	if len(b) < 116 || string(b[16:20]) != "MOBI" {
		return h, ErrFormat
	}
	length := int64(binary.BigEndian.Uint32(b[20:]))
	if length < 24 || length+16 > int64(len(b)) {
		return h, ErrFormat
	}
	h.encoding = binary.BigEndian.Uint32(b[28:])
	h.version = binary.BigEndian.Uint32(b[36:])
	if h.encoding != 1252 && h.encoding != 65001 {
		return h, fmt.Errorf("%w: encoding %d", ErrUnsupported, h.encoding)
	}
	if h.version > 8 {
		return h, fmt.Errorf("%w: version %d", ErrUnsupported, h.version)
	}
	toff, tlen := int64(binary.BigEndian.Uint32(b[84:])), int64(binary.BigEndian.Uint32(b[88:]))
	if tlen > 2048 {
		return h, ErrLimit
	}
	if tlen > 0 {
		if toff < 0 || toff+tlen > int64(len(b)) {
			return h, ErrFormat
		}
		h.title = decodeTitle(b[toff:toff+tlen], h.encoding)
	}
	if length >= 0xe4 && h.version >= 5 {
		if len(b) < 0xf4 {
			return h, ErrFormat
		}
		h.extra = binary.BigEndian.Uint16(b[0xf2:])
	}
	if h.compression == 0x4448 {
		if length+16 < 0x78 {
			return h, ErrFormat
		}
		off, count := uint64(binary.BigEndian.Uint32(b[0x70:])), uint64(binary.BigEndian.Uint32(b[0x74:]))
		if count < 2 || count > 1024 || off+count+uint64(start) > uint64(len(db.offsets)-1) {
			return h, ErrFormat
		}
		h.huffStart = int(off) + start
		h.huffCount = int(count)
	}
	if length+16 >= 0xf8 {
		h.ncx = binary.BigEndian.Uint32(b[0xf4:])
	}
	if h.version == 8 {
		if length+16 < 0x100 {
			return h, ErrFormat
		}
		h.frag = binary.BigEndian.Uint32(b[0xf8:])
		h.skel = binary.BigEndian.Uint32(b[0xfc:])
		if binary.BigEndian.Uint32(b[0xc4:]) > 1 {
			h.fdst = binary.BigEndian.Uint32(b[0xc0:])
		}
	}
	return h, nil
}

func trimRecord(b []byte, flags uint16) ([]byte, error) {
	for bits := flags >> 1; bits > 0; bits >>= 1 {
		if bits&1 == 0 {
			continue
		}
		n := uint32(0)
		terminated := false
		for i := len(b) - 1; i >= 0 && i >= len(b)-4; i-- {
			n |= uint32(b[i]&127) << uint((len(b)-1-i)*7)
			if b[i]&128 != 0 {
				terminated = true
				break
			}
		}
		if !terminated || n == 0 || uint64(n) > uint64(len(b)) {
			return nil, ErrFormat
		}
		b = b[:len(b)-int(n)]
	}
	if flags&1 != 0 {
		if len(b) == 0 {
			return nil, ErrFormat
		}
		n := int(b[len(b)-1]&3) + 1
		if n > len(b) {
			return nil, ErrFormat
		}
		b = b[:len(b)-n]
	}
	return b, nil
}

func decodeTitle(b []byte, encoding uint32) string {
	if encoding == 65001 {
		return strings.ToValidUTF8(string(b), "\ufffd")
	}
	const cp1252 = "€\u0081‚ƒ„…†‡ˆ‰Š‹Œ\u008dŽ\u008f\u0090‘’“”•–—˜™š›œ\u009džŸ"
	special := []rune(cp1252)
	var out strings.Builder
	for _, v := range b {
		r := rune(v)
		if v >= 128 && v <= 159 {
			r = special[v-128]
		}
		out.WriteRune(r)
	}
	return out.String()
}

type escapeReader struct {
	r       io.Reader
	pending []byte
	err     error
	carryCR bool
}

func (r *escapeReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		var buf [4097]byte
		start := 0
		if r.carryCR {
			buf[0] = '\r'
			start = 1
			r.carryCR = false
		}
		n, err := r.r.Read(buf[start : start+4096])
		if n == 0 && err == nil {
			return 0, io.ErrNoProgress
		}
		r.err = err
		n += start
		// A separator between CR and LF would turn one HTML newline into two.
		if err == nil && n > 0 && buf[n-1] == '\r' {
			r.carryCR = true
			n--
		}
		if n > 0 {
			b := bytes.ReplaceAll(buf[:n], []byte("&"), []byte("&amp;"))
			b = bytes.ReplaceAll(b, []byte("<"), []byte("&lt;"))
			r.pending = bytes.ReplaceAll(b, []byte(">"), []byte("&gt;"))
			// Comments bound tokenizer text tokens without adding visible content.
			r.pending = append(r.pending, "<!-- -->"...)
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func validTitle(b []byte, enc uint32) string {
	if len(b) > 2048 {
		b = b[:2048]
	}
	return strings.TrimSpace(decodeTitle(b, enc))
}
