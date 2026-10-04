package mobiformat

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put32(b []byte, at int, v uint32) { binary.BigEndian.PutUint32(b[at:], v) }
func testHeader(version uint32, compression uint16, textSize int, records int) []byte {
	b := make([]byte, 280+4)
	binary.BigEndian.PutUint16(b, compression)
	put32(b, 4, uint32(textSize))
	binary.BigEndian.PutUint16(b[8:], uint16(records))
	binary.BigEndian.PutUint16(b[10:], 4096)
	copy(b[16:], "MOBI")
	put32(b, 20, 264)
	put32(b, 24, 2)
	put32(b, 28, 65001)
	put32(b, 36, version)
	put32(b, 84, 280)
	put32(b, 88, 4)
	copy(b[280:], "Test")
	for _, offset := range []int{0xf4, 0xf8, 0xfc, 0x104} {
		put32(b, offset, absent)
	}
	return b
}
func testDatabase(records ...[]byte) []byte {
	b := make([]byte, 78+len(records)*8)
	copy(b, "Test")
	copy(b[60:], "BOOKMOBI")
	binary.BigEndian.PutUint16(b[76:], uint16(len(records)))
	for i, record := range records {
		put32(b, 78+i*8, uint32(len(b)))
		b = append(b, record...)
	}
	return b
}
func varint(v uint32) []byte {
	b := []byte{byte(v&127) | 128}
	v >>= 7
	for v > 0 {
		b = append([]byte{byte(v & 127)}, b...)
		v >>= 7
	}
	return b
}
func testIndex(defs []byte, entries ...[]byte) ([]byte, []byte) {
	main := make([]byte, 192)
	copy(main, "INDX")
	put32(main, 4, 192)
	put32(main, 24, 1)
	put32(main, 28, 65001)
	tag := make([]byte, 12)
	copy(tag, "TAGX")
	put32(tag, 4, uint32(12+len(defs)))
	put32(tag, 8, 1)
	main = append(main, tag...)
	main = append(main, defs...)
	data := make([]byte, 192)
	copy(data, "INDX")
	put32(data, 4, 192)
	put32(data, 24, uint32(len(entries)))
	put32(data, 28, 65001)
	positions := make([]uint16, len(entries))
	for i, entry := range entries {
		positions[i] = uint16(len(data))
		data = append(data, entry...)
	}
	put32(data, 20, uint32(len(data)))
	data = append(data, []byte("IDXT")...)
	for _, pos := range positions {
		data = append(data, byte(pos>>8), byte(pos))
	}
	return main, data
}
func testEntry(label string, control byte, values ...uint32) []byte {
	b := append([]byte{byte(len(label))}, []byte(label)...)
	b = append(b, control)
	for _, v := range values {
		b = append(b, varint(v)...)
	}
	return b
}
func testKF8Records() [][]byte {
	skeleton := "<html><body></body></html>"
	fragment := "<p>One 中文</p>"
	raw := []byte(skeleton + fragment + "p{color:red}")
	skel, skelData := testIndex([]byte{1, 1, 1, 0, 6, 2, 2, 0, 0, 0, 0, 1}, testEntry("0", 3, 1, 0, uint32(len(skeleton))))
	frag, fragData := testIndex([]byte{3, 1, 1, 0, 4, 1, 2, 0, 6, 2, 4, 0, 0, 0, 0, 1}, testEntry("12", 7, 0, 0, 0, uint32(len(fragment))))
	header := testHeader(8, 1, len(raw), 1)
	put32(header, 0xfc, 2)
	put32(header, 0xf8, 4)
	put32(header, 0xc0, 6)
	put32(header, 0xc4, 2)
	fdst := make([]byte, 28)
	copy(fdst, "FDST")
	put32(fdst, 4, 12)
	put32(fdst, 8, 2)
	put32(fdst, 16, uint32(len(skeleton+fragment)))
	put32(fdst, 20, uint32(len(skeleton+fragment)))
	put32(fdst, 24, uint32(len(raw)))
	return [][]byte{header, raw, skel, skelData, frag, fragData, fdst}
}
func collect(t *testing.T, b []byte) ([]string, error) {
	t.Helper()
	var parts []string
	dir := t.TempDir()
	err := Extract(bytes.NewReader(b), int64(len(b)), dir, func(title string, encoding uint32, r io.Reader) error {
		if encoding != 65001 && encoding != 1252 {
			t.Errorf("unexpected encoding %d", encoding)
		}
		data, e := io.ReadAll(r)
		parts = append(parts, string(data))
		return e
	})
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	if len(entries) != 0 {
		t.Fatal("temporary raw text leaked")
	}
	return parts, err
}
func TestLegacyAndRecordBoundary(t *testing.T) {
	body := "<html><body><p>你好 &amp; world</p></body></html>"
	b := testDatabase(testHeader(6, 1, len(body), 2), []byte(body[:17]), []byte(body[17:]))
	parts, err := collect(t, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0] != body {
		t.Fatalf("wrong HTML: %q", parts)
	}
}
func TestKF8ReconstructAndSkipCSS(t *testing.T) {
	records := testKF8Records()
	parts, err := collect(t, testDatabase(records...))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0] != "<html><body><p>One 中文</p></body></html>" {
		t.Fatalf("wrong reconstruction: %q", parts)
	}
}
func TestMixedPrefersKF8(t *testing.T) {
	legacy := []byte("<html><body>OLD</body></html>")
	records := [][]byte{testHeader(6, 1, len(legacy), 1), legacy, []byte("BOUNDARY")}
	records = append(records, testKF8Records()...)
	parts, err := collect(t, testDatabase(records...))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || strings.Contains(parts[0], "OLD") || !strings.Contains(parts[0], "中文") {
		t.Fatalf("wrong edition: %q", parts)
	}
}
func TestRejectedHeadersAndCleanup(t *testing.T) {
	body := []byte("<html><body>x</body></html>")
	tests := []struct {
		name   string
		mutate func([]byte)
		want   error
	}{
		{"DRM", func(b []byte) { binary.BigEndian.PutUint16(b[12:], 1) }, ErrDRM},
		{"encoding", func(b []byte) { put32(b, 28, 999) }, ErrUnsupported},
		{"compression", func(b []byte) { binary.BigEndian.PutUint16(b, 3) }, ErrUnsupported},
		{"text length", func(b []byte) { put32(b, 4, 1) }, ErrFormat},
		{"expansion", func(b []byte) { put32(b, 4, maxText+1) }, ErrLimit},
		{"KF8 missing index", func(b []byte) { put32(b, 36, 8) }, ErrUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := testHeader(6, 1, len(body), 1)
			tt.mutate(h)
			_, e := collect(t, testDatabase(h, body))
			if !errors.Is(e, tt.want) {
				t.Fatalf("got %v, want %v", e, tt.want)
			}
		})
	}
}
func TestCallbackFailure(t *testing.T) {
	body := []byte("<html><body>x</body></html>")
	b := testDatabase(testHeader(6, 1, len(body), 1), body)
	dir := t.TempDir()
	sentinel := errors.New("callback stopped")
	err := Extract(bytes.NewReader(b), int64(len(b)), dir, func(string, uint32, io.Reader) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("leaked temp")
	}
}
func TestTruncatedInputsNeverPanic(t *testing.T) {
	b := testDatabase(testKF8Records()...)
	for n := 0; n < len(b); n += 7 {
		_, _ = collect(t, b[:n])
	}
}
func TestPalmDOC(t *testing.T) {
	tests := []struct {
		in   []byte
		out  string
		fail bool
	}{
		{[]byte{3, 'a', 'b', 'c'}, "abc", false},
		{[]byte{'a', 0x80, 0x0a}, "aaaaaa", false},
		{[]byte{0xc1, 0}, " A\x00", false},
		{[]byte{3, 'a'}, "", true},
		{[]byte{0x80}, "", true},
		{[]byte{0x80, 0}, "", true},
		{[]byte{0x80, 0x10}, "", true},
	}
	for _, tt := range tests {
		out, e := palmDOC(tt.in)
		if tt.fail {
			if e == nil {
				t.Fatalf("accepted %x", tt.in)
			}
		} else if e != nil || string(out) != tt.out {
			t.Fatalf("%x -> %q, %v", tt.in, out, e)
		}
	}
}
func TestTrimRecord(t *testing.T) {
	b, e := trimRecord([]byte{'a', 'b', 'x', 'y', 0x83}, 2)
	if e != nil || string(b) != "ab" {
		t.Fatalf("%q %v", b, e)
	}
	b, e = trimRecord([]byte{'a', 'b', 1}, 1)
	if e != nil || string(b) != "a" {
		t.Fatalf("%q %v", b, e)
	}
	for _, b := range [][]byte{nil, {0}, {0x8f}} {
		if _, e = trimRecord(b, 2); e == nil {
			t.Fatalf("accepted %x", b)
		}
	}
}
func TestHUFFLiteralRecursiveAndCycle(t *testing.T) {
	h := &huffman{phrases: []huffPhrase{{[]byte("A"), true}, {[]byte("B"), true}}}
	for i := range h.table {
		h.table[i] = huffEntry{1, true, 0xffffffff}
	}
	out, e := h.unpack([]byte{0xaa})
	if e != nil || string(out) != "ABABABAB" {
		t.Fatalf("%q %v", out, e)
	}
	h.phrases[0] = huffPhrase{[]byte{0}, false}
	out, e = h.unpack([]byte{0xff})
	if e != nil || string(out) != strings.Repeat("B", 64) {
		t.Fatalf("recursive: %q %v", out, e)
	}
	h.phrases[0] = huffPhrase{[]byte{0xff}, false}
	if _, e = h.unpack([]byte{0xff}); e == nil {
		t.Fatal("cycle accepted")
	}
	h.phrases[0] = huffPhrase{bytes.Repeat([]byte{'a'}, 10000), true}
	if _, e = h.unpack([]byte{0xff}); !errors.Is(e, ErrLimit) {
		t.Fatalf("expansion: %v", e)
	}
}
func TestVariableAndTagByteCount(t *testing.T) {
	for _, v := range []uint32{0, 127, 128, 65535, 0xffffffff} {
		p := 0
		got, e := variable(varint(v), &p)
		if e != nil || got != v {
			t.Fatalf("%d %v", got, e)
		}
	}
	p := 0
	if _, e := variable([]byte{127, 127, 127, 127, 255}, &p); e == nil {
		t.Fatal("overflow accepted")
	}
	tags, e := parseTags([]byte{3, 0x82, 0x81, 0x82}, 1, []tagDefinition{{6, 2, 3, 0}})
	if e != nil || len(tags[6]) != 2 || tags[6][1] != 2 {
		t.Fatalf("%v %v", tags, e)
	}
}
func TestSpanInsertion(t *testing.T) {
	work := 0
	s, e := insertSpan([]span{{0, 6}}, 3, span{6, 2}, &work)
	if e != nil {
		t.Fatal(e)
	}
	r := &spanReader{source: strings.NewReader("abcdefXY"), parts: s}
	b, e := io.ReadAll(r)
	if e != nil || string(b) != "abcXYdef" {
		t.Fatalf("%q %v", b, e)
	}
	if _, e := insertSpan(s, 9, span{}, &work); e == nil {
		t.Fatal("out of range insertion")
	}
}

// External upstream fixtures remain outside the source tree; their distribution
// rights are not implied by libmobi's source license.
func TestUpstreamSamples(t *testing.T) {
	root := os.Getenv("MOBI_TEST_SAMPLES")
	if root == "" {
		t.Skip("set MOBI_TEST_SAMPLES to libmobi/tests/samples")
	}
	tests := []struct {
		name     string
		parts    int
		encoding uint32
		want     error
	}{
		{"sample-cp1252.mobi", 1, 1252, nil},
		{"sample-unicode-huffdic.mobi", 2, 65001, nil},
		{"sample-unicode-uncompressed.mobi", 2, 65001, nil},
		{"sample-drm-v1.mobi", 0, 0, ErrDRM},
		{"sample-drm_pidLTKULBB^5V-v2.mobi", 0, 0, ErrDRM},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, e := os.Open(filepath.Join(root, tt.name))
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			st, e := f.Stat()
			if e != nil {
				t.Fatal(e)
			}
			count := 0
			var text strings.Builder
			e = Extract(f, st.Size(), t.TempDir(), func(title string, encoding uint32, r io.Reader) error {
				count++
				if encoding != tt.encoding {
					t.Fatalf("encoding %d", encoding)
				}
				_, e := io.Copy(&text, r)
				return e
			})
			if !errors.Is(e, tt.want) {
				t.Fatalf("extract: %v", e)
			}
			if count != tt.parts {
				t.Fatalf("got %d parts, wanted %d", count, tt.parts)
			}
			if tt.want == nil && !strings.Contains(strings.ToLower(text.String()), "libmobi") {
				t.Fatal("missing body text")
			}
		})
	}
}

func testHUFFRecords() [][]byte {
	body := []byte("<html><body>HUFF 中文</body></html>")
	h := testHeader(6, 0x4448, len(body), 1)
	put32(h, 0x70, 2)
	put32(h, 0x74, 2)
	table := make([]byte, 1304)
	copy(table, "HUFF")
	put32(table, 4, 24)
	put32(table, 8, 24)
	put32(table, 12, 1048)
	for i := 0; i < 256; i++ {
		put32(table, 24+i*4, 0xff88)
	}
	dictionary := make([]byte, 16+512+256*3)
	copy(dictionary, "CDIC")
	put32(dictionary, 4, 16)
	put32(dictionary, 8, 256)
	put32(dictionary, 12, 8)
	for i := 0; i < 256; i++ {
		off := 512 + i*3
		binary.BigEndian.PutUint16(dictionary[16+i*2:], uint16(off))
		binary.BigEndian.PutUint16(dictionary[16+off:], 0x8001)
		dictionary[18+off] = byte(255 - i)
	}
	return [][]byte{h, body, table, dictionary}
}

func TestHUFFTableLoader(t *testing.T) {
	parts, err := collect(t, testDatabase(testHUFFRecords()...))
	if err != nil || len(parts) != 1 || parts[0] != "<html><body>HUFF 中文</body></html>" {
		t.Fatalf("%q %v", parts, err)
	}
	for _, test := range []struct {
		name   string
		mutate func([][]byte)
	}{
		{"bad HUFF signature", func(r [][]byte) { r[2][0] = 'x' }},
		{"bad table offset", func(r [][]byte) { put32(r[2], 8, 0xffffffff) }},
		{"zero code length", func(r [][]byte) { put32(r[2], 24, 0) }},
		{"bad CDIC signature", func(r [][]byte) { r[3][0] = 'x' }},
		{"excess phrases", func(r [][]byte) { put32(r[3], 8, 0xffffffff) }},
		{"excess code bits", func(r [][]byte) { put32(r[3], 12, 31) }},
		{"invalid phrase offset", func(r [][]byte) { binary.BigEndian.PutUint16(r[3][16:], 0xffff) }},
		{"invalid phrase length", func(r [][]byte) { binary.BigEndian.PutUint16(r[3][16+512:], 0xffff) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			records := testHUFFRecords()
			test.mutate(records)
			if _, err := collect(t, testDatabase(records...)); err == nil {
				t.Fatal("accepted corrupt dictionary")
			}
		})
	}
}

func TestHUFFIndexDoesNotOverflowMIPS(t *testing.T) {
	h := &huffman{phrases: []huffPhrase{{[]byte("x"), true}}}
	for i := range h.table {
		h.table[i] = huffEntry{32, true, 0xffffffff}
	}
	if _, err := h.unpack([]byte{0, 0, 0, 0}); err == nil {
		t.Fatal("accepted invalid dictionary index")
	}
}

func TestPalmDOCPlainText(t *testing.T) {
	body := []byte("A < B & C > D")
	h := make([]byte, 16)
	binary.BigEndian.PutUint16(h, 1)
	put32(h, 4, uint32(len(body)))
	binary.BigEndian.PutUint16(h[8:], 1)
	b := testDatabase(h, body)
	copy(b[60:], "TEXtREAd")
	parts, err := collect(t, b)
	if err != nil || len(parts) != 1 || parts[0] != "<html><body><pre>A &lt; B &amp; C &gt; D<!-- --></pre></body></html>" {
		t.Fatalf("%q %v", parts, err)
	}
}

type generatedSource struct {
	prefix  []byte
	size    int64
	largest int
}

func (s *generatedSource) ReadAt(p []byte, off int64) (int, error) {
	if len(p) > 256<<10 {
		return 0, ErrLimit
	}
	if len(p) > s.largest {
		s.largest = len(p)
	}
	if off < 0 || off >= s.size {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > s.size-off {
		n = int(s.size - off)
	}
	for i := 0; i < n; i++ {
		at := off + int64(i)
		if at < int64(len(s.prefix)) {
			p[i] = s.prefix[at]
		} else {
			p[i] = 'x'
		}
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func TestLargeTextUsesBoundedSourceReads(t *testing.T) {
	const records = 16384
	const recordSize = 4096
	header := testHeader(6, 1, records*recordSize, records)
	tableSize := 78 + (records+1)*8
	prefix := make([]byte, tableSize+len(header))
	copy(prefix, "Large")
	copy(prefix[60:], "BOOKMOBI")
	binary.BigEndian.PutUint16(prefix[76:], records+1)
	put32(prefix, 78, uint32(tableSize))
	copy(prefix[tableSize:], header)
	for i := 1; i <= records; i++ {
		put32(prefix, 78+i*8, uint32(len(prefix)+(i-1)*recordSize))
	}
	source := &generatedSource{prefix: prefix, size: int64(len(prefix) + records*recordSize)}
	var count int64
	err := Extract(source, source.size, t.TempDir(), func(_ string, _ uint32, r io.Reader) error { var e error; count, e = io.Copy(io.Discard, r); return e })
	if err != nil || count != records*recordSize {
		t.Fatalf("read %d: %v", count, err)
	}
	if source.largest > 256<<10 {
		t.Fatal("unbounded source read")
	}
}

func FuzzExtract(f *testing.F) {
	f.Add(testDatabase(testHeader(6, 1, 1, 1), []byte("x")))
	f.Add(testDatabase(testKF8Records()...))
	f.Add(testDatabase(testHUFFRecords()...))
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			t.Skip()
		}
		_ = Extract(bytes.NewReader(b), int64(len(b)), dir, func(_ string, _ uint32, r io.Reader) error { _, e := io.Copy(io.Discard, r); return e })
	})
}
