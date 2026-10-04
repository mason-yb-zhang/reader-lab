// SPDX-License-Identifier: GPL-3.0-only
// KF8 reconstruction adapted from KindleUnpack (GPLv3), lib/mobi_k8proc.py
// and mobi_ncx.py, revision bf0ca6e. Original copyright holders: extract.go.
package mobiformat

import (
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
)

type span struct{ offset, length int64 }

func insertSpan(parts []span, position int64, value span, work *int) ([]span, error) {
	if position < 0 {
		return nil, ErrFormat
	}
	// Charge allocation and copying even when insertion needs no searching.
	cost := 2 * (len(parts) + 2)
	if cost > 1000000-*work {
		return nil, ErrLimit
	}
	*work += cost
	for i, s := range parts {
		*work++
		if *work > 1000000 {
			return nil, ErrLimit
		}
		if position <= s.length {
			next := make([]span, 0, len(parts)+2)
			next = append(next, parts[:i]...)
			if position > 0 {
				next = append(next, span{s.offset, position})
			}
			if value.length > 0 {
				next = append(next, value)
			}
			if position < s.length {
				next = append(next, span{s.offset + position, s.length - position})
			}
			next = append(next, parts[i+1:]...)
			return next, nil
		}
		position -= s.length
	}
	if position == 0 {
		return append(parts, value), nil
	}
	return nil, ErrFormat
}

type spanReader struct {
	source  io.ReaderAt
	parts   []span
	current *io.SectionReader
}

func (r *spanReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if r.current == nil {
			if len(r.parts) == 0 {
				return 0, io.EOF
			}
			s := r.parts[0]
			r.parts = r.parts[1:]
			r.current = io.NewSectionReader(r.source, s.offset, s.length)
		}
		n, err := r.current.Read(p)
		if err == io.EOF {
			r.current = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (db *database) extractKF8(raw io.ReaderAt, total int64, h header, visit func(string, uint32, io.Reader) error) error {
	if h.skel == absent || h.frag == absent {
		return fmt.Errorf("%w: KF8 skeleton/fragment indices missing", ErrUnsupported)
	}
	flowLength := total
	if h.fdst != absent {
		record := int64(h.fdst) + int64(h.start)
		if record >= int64(len(db.offsets)-1) {
			return ErrFormat
		}
		b, err := db.record(int(record))
		if err != nil {
			return err
		}
		if len(b) < 12 || string(b[:4]) != "FDST" {
			return ErrFormat
		}
		headerLen := int64(binary.BigEndian.Uint32(b[4:]))
		count := int64(binary.BigEndian.Uint32(b[8:]))
		if headerLen != 12 || count < 1 || count > maxEntries || count*8+12 > int64(len(b)) {
			return ErrFormat
		}
		var previous int64
		for i := int64(0); i < count; i++ {
			begin, end := int64(binary.BigEndian.Uint32(b[12+i*8:])), int64(binary.BigEndian.Uint32(b[16+i*8:]))
			if begin < previous || end < begin || end > total {
				return ErrFormat
			}
			if i == 0 {
				if begin != 0 {
					return ErrFormat
				}
				flowLength = end
			}
			previous = end
		}
	}
	skeleton, err := db.readIndex(h.skel, h)
	if err != nil {
		return fmt.Errorf("KF8 skeleton: %w", err)
	}
	fragments, err := db.readIndex(h.frag, h)
	if err != nil {
		return fmt.Errorf("KF8 fragments: %w", err)
	}
	if len(skeleton.entries) == 0 {
		return ErrFormat
	}
	titles := make(map[int]string)
	if h.ncx != absent {
		ncx, e := db.readIndex(h.ncx, h)
		if e != nil {
			return fmt.Errorf("KF8 navigation: %w", e)
		}
		for _, entry := range ncx.entries {
			name, exists := entry.tags[3]
			if !exists || len(name) != 1 {
				continue
			}
			fid, exists := entry.tags[6]
			if !exists || len(fid) != 2 {
				continue
			}
			if uint64(fid[0]) >= uint64(len(fragments.entries)) {
				return ErrFormat
			}
			file, e := tagValue(fragments.entries[int(fid[0])], 3, 0)
			if e != nil {
				return e
			}
			if file >= int64(len(skeleton.entries)) {
				return ErrFormat
			}
			if titles[int(file)] == "" {
				titles[int(file)] = validTitle(ncx.names[name[0]], h.encoding)
			}
		}
	}
	fragment, work := 0, 0
	var emitted, previous int64
	for part, entry := range skeleton.entries {
		count, e := tagValue(entry, 1, 0)
		if e != nil {
			return e
		}
		start, e := tagValue(entry, 6, 0)
		if e != nil {
			return e
		}
		length, e := tagValue(entry, 6, 1)
		if e != nil {
			return e
		}
		if count > int64(len(fragments.entries)-fragment) || start < previous || start+length > flowLength {
			return ErrFormat
		}
		spans := []span{{start, length}}
		rawPosition := start + length
		outputLength := length
		for j := int64(0); j < count; j++ {
			f := fragments.entries[fragment]
			position, e := strconv.ParseUint(f.label, 10, 32)
			if e != nil {
				return fmt.Errorf("%w: fragment position", ErrFormat)
			}
			file, e := tagValue(f, 3, 0)
			if e != nil {
				return e
			}
			size, e := tagValue(f, 6, 1)
			if e != nil {
				return e
			}
			if file != int64(part) || rawPosition+size > flowLength || int64(position) < start {
				return ErrFormat
			}
			spans, e = insertSpan(spans, int64(position)-start, span{rawPosition, size}, &work)
			if e != nil {
				return fmt.Errorf("KF8 insertion: %w", e)
			}
			rawPosition += size
			outputLength += size
			fragment++
		}
		previous = rawPosition
		emitted += outputLength
		if emitted > maxText {
			return ErrLimit
		}
		title := titles[part]
		if title == "" && part == 0 {
			title = h.title
		}
		if err = visit(title, h.encoding, &spanReader{source: raw, parts: spans}); err != nil {
			return err
		}
	}
	if fragment != len(fragments.entries) {
		return fmt.Errorf("%w: unused KF8 fragments", ErrFormat)
	}
	return nil
}
