// SPDX-License-Identifier: GPL-3.0-only
// INDX/TAGX algorithms adapted from KindleUnpack (GPLv3), lib/mobi_index.py,
// revision bf0ca6e. Original copyright holders are listed in extract.go.
package mobiformat

import (
	"encoding/binary"
	"math/bits"
)

type indexEntry struct {
	label string
	tags  map[byte][]uint32
}
type mobiIndex struct {
	entries []indexEntry
	names   map[uint32][]byte
}
type tagDefinition struct{ tag, values, mask, end byte }

func (db *database) indexRecord(record int) ([]byte, error) {
	if record < 0 || record+1 >= len(db.offsets) {
		return nil, ErrFormat
	}
	n := db.offsets[record+1] - db.offsets[record]
	if db.indexBytes+n > maxIndexBytes {
		return nil, ErrLimit
	}
	db.indexBytes += n
	return db.record(record)
}

func variable(b []byte, pos *int) (uint32, error) {
	var value uint32
	for n := 0; n < 5; n++ {
		if *pos >= len(b) {
			return 0, ErrFormat
		}
		v := b[*pos]
		*pos++
		if value > 0x1ffffff {
			return 0, ErrFormat
		}
		value = value<<7 | uint32(v&127)
		if v&128 != 0 {
			return value, nil
		}
	}
	return 0, ErrFormat
}

func indexHeader(b []byte) (length, start, count, names int, err error) {
	if len(b) < 56 || string(b[:4]) != "INDX" {
		return 0, 0, 0, 0, ErrFormat
	}
	length64 := int64(binary.BigEndian.Uint32(b[4:]))
	start64 := int64(binary.BigEndian.Uint32(b[20:]))
	count64 := int64(binary.BigEndian.Uint32(b[24:]))
	names64 := int64(binary.BigEndian.Uint32(b[52:]))
	if length64 < 56 || length64 > int64(len(b)) || start64 > int64(len(b)) {
		return 0, 0, 0, 0, ErrFormat
	}
	if count64 > maxEntries || names64 > 64 {
		return 0, 0, 0, 0, ErrLimit
	}
	if binary.BigEndian.Uint32(b[28:]) == 65002 {
		return 0, 0, 0, 0, ErrUnsupported
	}
	if length64 >= 0xb8 && (binary.BigEndian.Uint32(b[0xa4:]) != 0 || binary.BigEndian.Uint32(b[0xa8:]) != 0) {
		return 0, 0, 0, 0, ErrUnsupported
	}
	return int(length64), int(start64), int(count64), int(names64), nil
}

func (db *database) readIndex(relative uint32, h header) (mobiIndex, error) {
	result := mobiIndex{names: make(map[uint32][]byte)}
	if relative == absent {
		return result, nil
	}
	record64 := int64(relative) + int64(h.start)
	if record64 >= int64(len(db.offsets)-1) {
		return result, ErrFormat
	}
	record := int(record64)
	b, err := db.indexRecord(record)
	if err != nil {
		return result, err
	}
	length, _, records, nameRecords, err := indexHeader(b)
	if err != nil {
		return result, err
	}
	if record+records+nameRecords >= len(db.offsets)-1 {
		return result, ErrFormat
	}
	if length+12 > len(b) || string(b[length:length+4]) != "TAGX" {
		return result, ErrFormat
	}
	tagSize := int64(binary.BigEndian.Uint32(b[length+4:]))
	controls := int64(binary.BigEndian.Uint32(b[length+8:]))
	if tagSize < 12 || (tagSize-12)%4 != 0 || int64(length)+tagSize > int64(len(b)) {
		return result, ErrFormat
	}
	if controls == 0 || controls > 32 || tagSize > 1036 {
		return result, ErrLimit
	}
	var tags []tagDefinition
	for i := length + 12; i < length+int(tagSize); i += 4 {
		tags = append(tags, tagDefinition{b[i], b[i+1], b[i+2], b[i+3]})
	}
	for i := 0; i < records; i++ {
		data, e := db.indexRecord(record + 1 + i)
		if e != nil {
			return result, e
		}
		headerLen, table, n, _, e := indexHeader(data)
		if e != nil {
			return result, e
		}
		if db.indexEntries+n > maxEntries {
			return result, ErrLimit
		}
		db.indexEntries += n
		if table < headerLen || table+4+n*2 > len(data) || string(data[table:table+4]) != "IDXT" {
			return result, ErrFormat
		}
		previous := headerLen
		for j := 0; j < n; j++ {
			begin := int(binary.BigEndian.Uint16(data[table+4+j*2:]))
			end := table
			if j+1 < n {
				end = int(binary.BigEndian.Uint16(data[table+6+j*2:]))
			}
			if begin < previous || end <= begin || end > table {
				return result, ErrFormat
			}
			previous = end
			size := int(data[begin])
			if begin+1+size > end {
				return result, ErrFormat
			}
			values, e := parseTags(data[begin+1+size:end], int(controls), tags)
			if e != nil {
				return result, e
			}
			result.entries = append(result.entries, indexEntry{string(data[begin+1 : begin+1+size]), values})
		}
	}
	for i := 0; i < nameRecords; i++ {
		data, e := db.indexRecord(record + 1 + records + i)
		if e != nil {
			return result, e
		}
		for pos := 0; pos < len(data) && data[pos] != 0; {
			start := pos
			size, e := variable(data, &pos)
			if e != nil {
				return result, e
			}
			if uint64(size) > uint64(len(data)-pos) {
				return result, ErrFormat
			}
			if size > 2048 {
				return result, ErrLimit
			}
			if len(result.names) >= maxEntries {
				return result, ErrLimit
			}
			result.names[uint32(i)*65536+uint32(start)] = data[pos : pos+int(size)]
			pos += int(size)
		}
	}
	return result, nil
}

type tagValueSpec struct {
	tag          byte
	count, bytes int
}

func parseTags(b []byte, controls int, tags []tagDefinition) (map[byte][]uint32, error) {
	if controls > len(b) {
		return nil, ErrFormat
	}
	pos, control := controls, 0
	var specs []tagValueSpec
	for _, t := range tags {
		if t.end == 1 {
			control++
			continue
		}
		if t.end != 0 || control >= controls || t.mask == 0 || t.values == 0 {
			return nil, ErrFormat
		}
		value := b[control] & t.mask
		if value == 0 {
			continue
		}
		spec := tagValueSpec{tag: t.tag, bytes: -1}
		if value == t.mask && bits.OnesCount8(t.mask) > 1 {
			size, err := variable(b, &pos)
			if err != nil {
				return nil, err
			}
			if uint64(size) > uint64(len(b)) {
				return nil, ErrFormat
			}
			spec.bytes = int(size)
		} else if value == t.mask {
			spec.count = int(t.values)
		} else {
			spec.count = int(value>>uint(bits.TrailingZeros8(t.mask))) * int(t.values)
		}
		if spec.count > 64 {
			return nil, ErrLimit
		}
		specs = append(specs, spec)
	}
	out := make(map[byte][]uint32)
	for _, spec := range specs {
		var values []uint32
		if spec.bytes >= 0 {
			if spec.bytes > len(b)-pos {
				return nil, ErrFormat
			}
			end := pos + spec.bytes
			for pos < end {
				value, err := variable(b[:end], &pos)
				if err != nil {
					return nil, err
				}
				values = append(values, value)
				if len(values) > 64 {
					return nil, ErrLimit
				}
			}
		} else {
			for i := 0; i < spec.count; i++ {
				v, err := variable(b, &pos)
				if err != nil {
					return nil, err
				}
				values = append(values, v)
			}
		}
		switch spec.tag {
		case 1, 2, 3, 4, 6:
			if _, ok := out[spec.tag]; ok {
				return nil, ErrFormat
			}
			out[spec.tag] = values
		}
	}
	for _, v := range b[pos:] {
		if v != 0 {
			return nil, ErrFormat
		}
	}
	return out, nil
}

func tagValue(e indexEntry, tag byte, element int) (int64, error) {
	values := e.tags[tag]
	if element >= len(values) {
		return 0, ErrFormat
	}
	return int64(values[element]), nil
}
