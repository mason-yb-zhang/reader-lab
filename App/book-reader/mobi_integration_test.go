package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"c1book-reader/internal/mobiformat"
)

// Fixture offsets follow KindleUnpack lib/mobi_sectioner.py (Palm directory),
// mobi_header.py (record-relative fields), mobi_index.py (INDX/TAGX/VWI), and
// mobi_k8proc.py (skeleton/fragment assembly). No upstream book bytes are embedded.
func mobiIntegrationDatabase(records ...[]byte) []byte {
	data := make([]byte, 78+8*len(records)+2)
	copy(data, "Independent integration fixture")
	copy(data[60:68], "BOOKMOBI")
	binary.BigEndian.PutUint16(data[76:78], uint16(len(records)))
	for i, record := range records {
		binary.BigEndian.PutUint32(data[78+8*i:], uint32(len(data)))
		binary.BigEndian.PutUint32(data[82+8*i:], uint32(i+1))
		data = append(data, record...)
	}
	return data
}

func mobiIntegrationHeader(version, encoding uint32, compression uint16, textLength, textRecords int) []byte {
	b := make([]byte, 0x108)
	binary.BigEndian.PutUint16(b, compression)
	binary.BigEndian.PutUint32(b[4:], uint32(textLength))
	binary.BigEndian.PutUint16(b[8:], uint16(textRecords))
	binary.BigEndian.PutUint16(b[10:], 4096)
	copy(b[16:20], "MOBI")
	binary.BigEndian.PutUint32(b[20:], 0xf8)
	binary.BigEndian.PutUint32(b[24:], 2)
	binary.BigEndian.PutUint32(b[28:], encoding)
	binary.BigEndian.PutUint32(b[32:], 1)
	binary.BigEndian.PutUint32(b[36:], version)
	for _, off := range []int{0x28, 0x2c, 0x30, 0x34, 0x38, 0x3c, 0x40, 0x44, 0x48, 0x4c, 0x6c, 0xa8, 0xc0, 0xc8, 0xd0, 0xe0, 0xf4, 0xf8, 0xfc, 0x100, 0x104} {
		binary.BigEndian.PutUint32(b[off:], 0xffffffff)
	}
	binary.BigEndian.PutUint32(b[0x50:], uint32(textRecords+1))
	binary.BigEndian.PutUint32(b[0x68:], version)
	return b
}

func mobiIntegrationPalmEncode(raw []byte) []byte {
	var encoded []byte
	for _, b := range raw {
		if b > 8 && b < 128 || b == 0 {
			encoded = append(encoded, b)
		} else {
			encoded = append(encoded, 1, b)
		}
	}
	return encoded
}

func mobiIntegrationLegacy(compression uint16, encoding uint32, html []byte) []byte {
	// Split inside a UTF-8 character to ensure decompression records are joined
	// before HTML/encoding decoding, rather than decoded independently.
	cut := len(html) / 2
	if at := bytes.Index(html, []byte("甲")); at >= 0 {
		cut = at + 1
	}
	a, b := html[:cut], html[cut:]
	if compression == 2 {
		a, b = mobiIntegrationPalmEncode(a), mobiIntegrationPalmEncode(b)
	}
	return mobiIntegrationDatabase(mobiIntegrationHeader(6, encoding, compression, len(html), 2), a, b)
}

func mobiIntegrationVWI(n int) []byte {
	b := []byte{byte(n&127) | 128}
	for n >>= 7; n != 0; n >>= 7 {
		b = append([]byte{byte(n & 127)}, b...)
	}
	return b
}

func mobiIntegrationIndexEntry(label string, control byte, values ...int) []byte {
	b := append([]byte{byte(len(label))}, []byte(label)...)
	b = append(b, control)
	for _, n := range values {
		b = append(b, mobiIntegrationVWI(n)...)
	}
	return b
}

func mobiIntegrationIndex(tags []byte, names int, entries ...[]byte) ([]byte, []byte) {
	meta := make([]byte, 192)
	copy(meta, "INDX")
	binary.BigEndian.PutUint32(meta[4:], 192)
	binary.BigEndian.PutUint32(meta[8:], 0)
	binary.BigEndian.PutUint32(meta[12:], 2)
	binary.BigEndian.PutUint32(meta[24:], 1)
	binary.BigEndian.PutUint32(meta[28:], 65001)
	binary.BigEndian.PutUint32(meta[36:], uint32(len(entries)))
	binary.BigEndian.PutUint32(meta[52:], uint32(names))
	tagx := make([]byte, 12)
	copy(tagx, "TAGX")
	binary.BigEndian.PutUint32(tagx[4:], uint32(12+len(tags)))
	binary.BigEndian.PutUint32(tagx[8:], 1)
	meta = append(meta, tagx...)
	meta = append(meta, tags...)
	data := make([]byte, 192)
	copy(data, "INDX")
	binary.BigEndian.PutUint32(data[4:], 192)
	binary.BigEndian.PutUint32(data[24:], uint32(len(entries)))
	binary.BigEndian.PutUint32(data[28:], 65001)
	var offsets []uint16
	for _, entry := range entries {
		offsets = append(offsets, uint16(len(data)))
		data = append(data, entry...)
	}
	binary.BigEndian.PutUint32(data[20:], uint32(len(data)))
	data = append(data, "IDXT"...)
	for _, off := range offsets {
		data = append(data, byte(off>>8), byte(off))
	}
	return meta, data
}

func mobiIntegrationKF8(hybrid bool) []byte {
	return mobiIntegrationKF8Bodies(hybrid, "<b>MIDDLE</b>", "FINAL 甲乙 &amp; done.")
}

func mobiIntegrationTextRecords(raw []byte, compression uint16) [][]byte {
	var records [][]byte
	for len(raw) > 0 {
		n := len(raw)
		if n > 4096 {
			n = 4096
		}
		record := raw[:n]
		if compression == 2 {
			record = mobiIntegrationPalmEncode(record)
		}
		records = append(records, record)
		raw = raw[n:]
	}
	return records
}

func mobiIntegrationKF8Bodies(hybrid bool, middle, last string) []byte {
	prefix := "<html><head></head><body><h1>第一章 重组</h1><p>LEFT "
	skeleton1 := prefix + " RIGHT</p></body></html>"
	hidden := "<title>HIDDEN-FRAGMENT</title>"
	skeleton2 := "<html><head></head><body><h1>第二章 完成</h1><p></p></body></html>"
	secondStart := len(skeleton1) + len(hidden) + len(middle)
	mainFlow := skeleton1 + hidden + middle + skeleton2 + last
	css := "body::before { content: 'HIDDEN-CSS-FLOW'; }"
	raw := []byte(mainFlow + css)
	skelMeta, skelData := mobiIntegrationIndex([]byte{1, 1, 1, 0, 6, 2, 2, 0, 0, 0, 0, 1}, 0,
		mobiIntegrationIndexEntry("s0", 3, 2, 0, len(skeleton1)),
		mobiIntegrationIndexEntry("s1", 3, 1, secondStart, len(skeleton2)))
	fragMeta, fragData := mobiIntegrationIndex([]byte{2, 1, 1, 0, 3, 1, 2, 0, 4, 1, 4, 0, 6, 2, 8, 0, 0, 0, 0, 1}, 1,
		mobiIntegrationIndexEntry(fmt.Sprint(len("<html><head>")), 15, 0, 0, 0, 0, len(hidden)),
		mobiIntegrationIndexEntry(fmt.Sprint(len(prefix)+len(hidden)), 15, 0, 0, 1, len(hidden), len(middle)),
		mobiIntegrationIndexEntry(fmt.Sprint(secondStart+strings.Index(skeleton2, "</p>")), 15, 0, 1, 2, 0, len(last)))
	ctocText := []byte("kindle:aid:0000")
	ctoc := append(mobiIntegrationVWI(len(ctocText)), ctocText...)
	fdst := make([]byte, 28)
	copy(fdst, "FDST")
	binary.BigEndian.PutUint32(fdst[4:], 12)
	binary.BigEndian.PutUint32(fdst[8:], 2)
	binary.BigEndian.PutUint32(fdst[16:], uint32(len(mainFlow)))
	binary.BigEndian.PutUint32(fdst[20:], uint32(len(mainFlow)))
	binary.BigEndian.PutUint32(fdst[24:], uint32(len(raw)))
	textRecords := mobiIntegrationTextRecords(raw, 1)
	header := mobiIntegrationHeader(8, 65001, 1, len(raw), len(textRecords))
	binary.BigEndian.PutUint32(header[0xfc:], uint32(len(textRecords)+1))
	binary.BigEndian.PutUint32(header[0xf8:], uint32(len(textRecords)+3))
	binary.BigEndian.PutUint32(header[0xc0:], uint32(len(textRecords)+6))
	binary.BigEndian.PutUint32(header[0xc4:], 2)
	records := append([][]byte{header}, textRecords...)
	records = append(records, skelMeta, skelData, fragMeta, fragData, ctoc, fdst)
	if hybrid {
		legacy := []byte("<html><body><h1>LEGACY-ONLY</h1><p>Do not duplicate this edition.</p></body></html>")
		h := mobiIntegrationHeader(6, 65001, 1, len(legacy), 1)
		binary.BigEndian.PutUint32(h[0x80:], 0x40)
		exth := make([]byte, 24)
		copy(exth, "EXTH")
		binary.BigEndian.PutUint32(exth[4:], 24)
		binary.BigEndian.PutUint32(exth[8:], 1)
		binary.BigEndian.PutUint32(exth[12:], 121)
		binary.BigEndian.PutUint32(exth[16:], 12)
		binary.BigEndian.PutUint32(exth[20:], 3)
		h = append(h, exth...)
		records = append([][]byte{h, legacy, []byte("BOUNDARY")}, records...)
	}
	return mobiIntegrationDatabase(records...)
}

func mobiIntegrationWrite(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mobiIntegrationRoundTrip(t *testing.T, source string) string {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("C1BOOK_READER_CACHE_DIR", cache)
	before := fileDigest(t, source)
	beforeEntries, err := os.ReadDir(filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := OpenDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Path != source || doc.Encoding != EncodingUTF8 || doc.contentPath() == source || !validChapterIndex(doc.Chapters, doc.Size) {
		t.Fatalf("invalid normalized identity/index: %+v", doc)
	}
	rel, err := filepath.Rel(cache, doc.contentPath())
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("normalized text escaped independent cache: %q (%v)", doc.contentPath(), err)
	}
	text, _, err := doc.ReadRange(0, doc.Size, 0)
	if err != nil || len(text) == 0 || !utf8.Valid(text) {
		t.Fatalf("invalid normalized text: bytes=%d err=%v", len(text), err)
	}
	pagesTotal := 0
	for i, chapter := range doc.Chapters {
		for start := chapter.Start; start < chapter.End; {
			pages, end, err := Paginate(doc, chapter, start, fixedFace{}, 40, 60)
			if err != nil || len(pages) == 0 || end <= start || end > chapter.End {
				t.Fatalf("chapter %d at %d: pages=%d end=%d err=%v", i, start, len(pages), end, err)
			}
			previous := start
			for _, page := range pages {
				if page.Start < previous || page.Start > page.End || page.End > end {
					t.Fatalf("invalid page range: %+v", page)
				}
				for _, line := range page.Lines {
					if !utf8.ValidString(line) {
						t.Fatal("page split a UTF-8 character")
					}
				}
				previous = page.End
			}
			pagesTotal += len(pages)
			start = end
		}
	}
	if pagesTotal == 0 {
		t.Fatal("no readable pages")
	}
	// Persist a real page offset before reopening; neither store may key on the
	// private normalized-text path instead of the original book identity.
	chapterIndex := len(doc.Chapters) - 1
	chapter := doc.Chapters[chapterIndex]
	pages, _, err := Paginate(doc, chapter, chapter.Start, fixedFace{}, 6, 20)
	if err != nil || len(pages) == 0 {
		t.Fatalf("persistence page: %v", err)
	}
	// The last page of a bounded chapter window can be partial, so compare an
	// interior page when the book spans more than one window.
	selected := len(pages) / 2
	offset := pages[selected].Start
	mark, err := fingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	progressDir, bookmarkDir := t.TempDir(), t.TempDir()
	wantProgress := Progress{Path: source, Fingerprint: mark, Chapter: chapterIndex, Offset: offset}
	if err := (ProgressStore{Dir: progressDir}).Save(wantProgress); err != nil {
		t.Fatal(err)
	}
	wantBookmarks := []Bookmark{{Chapter: chapterIndex, Offset: offset}}
	if err := (BookmarkStore{Dir: bookmarkDir}).Save(source, wantBookmarks); err != nil {
		t.Fatal(err)
	}
	cacheInfo, err := os.Stat(doc.contentPath())
	if err != nil {
		t.Fatal(err)
	}
	again, err := OpenDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	againInfo, err := os.Stat(again.contentPath())
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(cacheInfo, againInfo) || !cacheInfo.ModTime().Equal(againInfo.ModTime()) {
		t.Fatal("cache reopen unnecessarily rewrote normalized content")
	}
	if doc.Size != again.Size || !reflect.DeepEqual(doc.Chapters, again.Chapters) || doc.contentPath() != again.contentPath() {
		t.Fatal("cache reopen changed document identity/index")
	}
	cachedText, _, err := again.ReadRange(0, again.Size, 0)
	if err != nil || !bytes.Equal(text, cachedText) {
		t.Fatalf("cache reopen changed text: %v", err)
	}
	saved, ok, err := (ProgressStore{Dir: progressDir}).Load(again.Path)
	if err != nil || !ok || saved.Path != source || saved.Fingerprint != mark || saved.Offset != offset || saved.Chapter != chapterIndex {
		t.Fatalf("progress after cache reopen: %+v ok=%v err=%v", saved, ok, err)
	}
	bookmarks, err := (BookmarkStore{Dir: bookmarkDir}).Load(again.Path)
	if err != nil || !reflect.DeepEqual(bookmarks, wantBookmarks) {
		t.Fatalf("bookmarks after cache reopen: %+v err=%v", bookmarks, err)
	}
	if at, ok := again.ChapterAtOffset(offset); !ok || at != chapterIndex {
		t.Fatal("persisted page offset no longer locates its chapter")
	}
	resumed, _, err := Paginate(again, again.Chapters[chapterIndex], saved.Offset, fixedFace{}, 6, 20)
	if err != nil || len(resumed) == 0 || !reflect.DeepEqual(resumed[0], pages[selected]) {
		t.Fatalf("resumed page differs from saved page: %v", err)
	}
	if err := os.Remove(doc.contentPath()); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := OpenDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	rebuiltText, _, err := rebuilt.ReadRange(0, rebuilt.Size, 0)
	if err != nil || !bytes.Equal(text, rebuiltText) || !reflect.DeepEqual(doc.Chapters, rebuilt.Chapters) {
		t.Fatalf("missing text cache did not rebuild deterministically: %v", err)
	}
	afterEntries, err := os.ReadDir(filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	if before != fileDigest(t, source) || !reflect.DeepEqual(beforeEntries, afterEntries) {
		t.Fatal("ingestion modified source bytes or created files beside the book")
	}
	t.Logf("sourceSHA256=%x textBytes=%d chapters=%d pages=%d cacheReused=true persistence=true", before, doc.Size, len(doc.Chapters), pagesTotal)
	return string(text)
}

func TestMOBIIntegrationLegacy(t *testing.T) {
	html := []byte("<html><head><title>HIDDEN-TITLE</title><style>HIDDEN-STYLE</style></head><body><h1>第一章 开始</h1><p>甲乙😀 &amp; café</p><h2>第二章 结束</h2><p>Final text.</p><script>HIDDEN-SCRIPT</script></body></html>")
	for _, compression := range []uint16{1, 2} {
		t.Run(fmt.Sprintf("compression-%d", compression), func(t *testing.T) {
			source := mobiIntegrationWrite(t, t.TempDir(), "book.MoBi", mobiIntegrationLegacy(compression, 65001, html))
			text := mobiIntegrationRoundTrip(t, source)
			if text != "第一章 开始\n甲乙😀 & café\n第二章 结束\nFinal text.\n" {
				t.Fatalf("unexpected legacy text: %q", text)
			}
		})
		t.Run(fmt.Sprintf("textread-compression-%d", compression), func(t *testing.T) {
			raw := []byte("Literal <tag> & caf\xe9")
			header := mobiIntegrationHeader(6, 1252, compression, len(raw), 1)[:16]
			textRecord := raw
			if compression == 2 {
				textRecord = mobiIntegrationPalmEncode(raw)
			}
			data := mobiIntegrationDatabase(header, textRecord)
			copy(data[60:68], "TEXtREAd")
			source := mobiIntegrationWrite(t, t.TempDir(), "palmdoc.mobi", data)
			if text := mobiIntegrationRoundTrip(t, source); text != "Literal <tag> & café\n" {
				t.Fatalf("PalmDOC plain text was treated as markup: %q", text)
			}
		})
	}
	t.Run("windows-1252", func(t *testing.T) {
		raw := []byte("<html><body><h1>Chapter</h1><p>caf\xe9 \x93quoted\x94 \x80</p></body></html>")
		source := mobiIntegrationWrite(t, t.TempDir(), "cp1252.mobi", mobiIntegrationLegacy(2, 1252, raw))
		if text := mobiIntegrationRoundTrip(t, source); text != "Chapter\ncafé “quoted” €\n" {
			t.Fatalf("codepage conversion: %q", text)
		}
	})
	t.Run("palmdoc-backreference-and-space", func(t *testing.T) {
		// PalmDOC 0x801b copies six bytes at distance three; 0xc1 emits " A".
		raw := []byte("<p>abcabcabc A</p>")
		compressed := append([]byte("<p>abc"), 0x80, 0x1b, 0xc1)
		compressed = append(compressed, "</p>"...)
		source := mobiIntegrationWrite(t, t.TempDir(), "backref.mobi", mobiIntegrationDatabase(mobiIntegrationHeader(6, 65001, 2, len(raw), 1), compressed))
		if text := mobiIntegrationRoundTrip(t, source); text != "abcabcabc A\n" {
			t.Fatalf("PalmDOC overlapping copy/space: %q", text)
		}
	})
}

func TestMOBIIntegrationKF8ReassemblyAndHybrid(t *testing.T) {
	for _, hybrid := range []bool{false, true} {
		t.Run(fmt.Sprintf("hybrid-%t", hybrid), func(t *testing.T) {
			name := "reassembled.AzW3"
			if hybrid {
				name = "hybrid.MOBI"
			}
			source := mobiIntegrationWrite(t, t.TempDir(), name, mobiIntegrationKF8(hybrid))
			text := mobiIntegrationRoundTrip(t, source)
			want := "第一章 重组\nLEFT MIDDLE RIGHT\n第二章 完成\nFINAL 甲乙 & done.\n"
			if text != want {
				t.Fatalf("KF8 skeleton/fragment order or flow filtering: got %q want %q", text, want)
			}
		})
	}
}

func TestMOBIIntegrationDRMAndCorruptInput(t *testing.T) {
	base := mobiIntegrationLegacy(1, 65001, []byte("<p>Readable body.</p>"))
	headerOffset := int(binary.BigEndian.Uint32(base[78:]))
	type fixtureCase struct {
		name string
		data []byte
		drm  bool
	}
	cases := []fixtureCase{
		{"empty", nil, false},
		{"truncated-palm-header", base[:77], false},
		{"truncated-record-directory", base[:90], false},
		{"truncated-text", base[:len(base)-3], false},
	}
	mutate := func(name string, drm bool, change func([]byte)) {
		data := append([]byte(nil), base...)
		change(data)
		cases = append(cases, fixtureCase{name, data, drm})
	}
	mutate("drm-v1", true, func(b []byte) { binary.BigEndian.PutUint16(b[headerOffset+12:], 1) })
	mutate("drm-v2", true, func(b []byte) { binary.BigEndian.PutUint16(b[headerOffset+12:], 2) })
	mutate("offset-inside-directory", false, func(b []byte) { binary.BigEndian.PutUint32(b[78:], 1) })
	mutate("offset-outside-file", false, func(b []byte) { binary.BigEndian.PutUint32(b[86:], uint32(len(b)+1)) })
	mutate("descending-offsets", false, func(b []byte) { binary.BigEndian.PutUint32(b[94:], uint32(headerOffset)) })
	mutate("bad-mobi-magic", false, func(b []byte) { b[headerOffset+16] = 'X' })
	mutate("oversized-header", false, func(b []byte) { binary.BigEndian.PutUint32(b[headerOffset+20:], 0xffffffff) })
	mutate("impossible-text-count", false, func(b []byte) { binary.BigEndian.PutUint16(b[headerOffset+8:], 0xffff) })
	mutate("text-length-mismatch", false, func(b []byte) { binary.BigEndian.PutUint32(b[headerOffset+4:], 999) })
	mutate("unsupported-compression", false, func(b []byte) { binary.BigEndian.PutUint16(b[headerOffset:], 3) })
	mutate("unsupported-codepage", false, func(b []byte) { binary.BigEndian.PutUint32(b[headerOffset+28:], 999) })
	for name, compressed := range map[string][]byte{"palmdoc-short-literal": {8, 'a'}, "palmdoc-short-reference": {0x80}, "palmdoc-invalid-distance": {0x80, 0x08}} {
		cases = append(cases, fixtureCase{name, mobiIntegrationDatabase(mobiIntegrationHeader(6, 65001, 2, 4, 1), compressed), false})
	}
	kf8 := mobiIntegrationKF8(false)
	skelOffset := int(binary.BigEndian.Uint32(kf8[78+2*8:]))
	kf8[skelOffset] = 'X'
	cases = append(cases, fixtureCase{"kf8-invalid-index", kf8, false})
	hybrid := mobiIntegrationKF8(true)
	kf8HeaderOffset := int(binary.BigEndian.Uint32(hybrid[78+3*8:]))
	binary.BigEndian.PutUint16(hybrid[kf8HeaderOffset+12:], 2)
	cases = append(cases, fixtureCase{"hybrid-encrypted-kf8", hybrid, true})
	for name, html := range map[string][]byte{"empty-body": []byte("<html><head><title>Hidden</title></head><body/></html>"), "invalid-utf8": {'<', 'p', '>', 0xff, '<', '/', 'p', '>'}} {
		cases = append(cases, fixtureCase{name, mobiIntegrationLegacy(1, 65001, html), false})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("C1BOOK_READER_CACHE_DIR", t.TempDir())
			source := mobiIntegrationWrite(t, t.TempDir(), "invalid.mobi", test.data)
			before := fileDigest(t, source)
			_, err := OpenDocument(source)
			if err == nil {
				t.Fatal("invalid or encrypted MOBI accepted")
			}
			if test.drm && (!errors.Is(err, mobiformat.ErrDRM) || !strings.Contains(strings.ToUpper(err.Error()), "DRM")) {
				t.Fatalf("DRM was not explicitly rejected: %v", err)
			}
			entries, readErr := os.ReadDir(documentCacheDir())
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if len(entries) != 0 || before != fileDigest(t, source) {
				t.Fatal("failed conversion left partial cache or changed source")
			}
		})
	}
}

func TestMOBIIntegrationScanLibraryCaseAndFolders(t *testing.T) {
	root := t.TempDir()
	names := []string{"top.MOBI", "deep/one/book.mObI", "deep/two/book.AzW3", "last.azw3", "old.TXT", "old.EPUB"}
	var want []string
	for _, name := range names {
		want = append(want, mobiIntegrationWrite(t, root, name, nil))
	}
	for _, name := range []string{"ignore.azw", "ignore.mobi.bak", "ignore.zip", "deep/book.azw3.name"} {
		mobiIntegrationWrite(t, root, name, nil)
	}
	if err := os.Mkdir(filepath.Join(root, "directory.mobi"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{".", "deep", "deep/one", "deep/two", "directory.mobi"} {
		dir := filepath.Join(root, folder)
		books, err := ScanLibrary(dir)
		if err != nil {
			t.Fatal(err)
		}
		var got, localWant, gotDirs []string
		for _, book := range books {
			if book.IsDir {
				if len(got) != 0 {
					t.Fatal("directory sorted after a book")
				}
				gotDirs = append(gotDirs, book.Name)
				continue
			}
			got = append(got, book.Path)
		}
		wantDirs := map[string][]string{".": {"deep", "directory.mobi"}, "deep": {"one", "two"}}[folder]
		if !reflect.DeepEqual(gotDirs, wantDirs) {
			t.Fatalf("folder entries in %s: got %v want %v", folder, gotDirs, wantDirs)
		}
		for _, path := range want {
			if filepath.Dir(path) == dir {
				localWant = append(localWant, path)
			}
		}
		sort.Slice(localWant, func(i, j int) bool { return strings.ToLower(localWant[i]) < strings.ToLower(localWant[j]) })
		if !reflect.DeepEqual(got, localWant) {
			t.Fatalf("single-folder case-insensitive discovery in %s: got %v want %v", folder, got, localWant)
		}
	}
}

// MOBI_TEST_SAMPLES is an opt-in local corpus (for example libmobi/tests/samples).
// Originals are only read; every ingestion runs on a private temporary copy.
func TestMOBIIntegrationSuppliedSamples(t *testing.T) {
	root := os.Getenv("MOBI_TEST_SAMPLES")
	if root == "" {
		t.Skip("set MOBI_TEST_SAMPLES to a local sample directory; no samples are downloaded or packaged")
	}
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ext := strings.ToLower(filepath.Ext(path))
		if entry.Type().IsRegular() && (ext == ".mobi" || ext == ".azw3" || ext == ".fail") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil || len(paths) == 0 {
		t.Fatalf("sample discovery: count=%d err=%v", len(paths), err)
	}
	for _, original := range paths {
		t.Run(filepath.Base(original), func(t *testing.T) {
			raw, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			before := sha256.Sum256(raw)
			defer func() {
				if before != fileDigest(t, original) {
					t.Fatal("opt-in source sample changed")
				}
			}()
			name := filepath.Base(original)
			if strings.EqualFold(filepath.Ext(name), ".fail") {
				name += ".mobi"
			}
			source := mobiIntegrationWrite(t, t.TempDir(), name, raw)
			// Diagnostics use upstream-defined offsets, not parser internals.
			drm := false
			if len(raw) >= 78 {
				n := int(binary.BigEndian.Uint16(raw[76:78]))
				if n > 0 && n <= (len(raw)-78)/8 {
					for i := 0; i < n; i++ {
						off := uint64(binary.BigEndian.Uint32(raw[78+8*i:]))
						if off+40 > uint64(len(raw)) || (i != 0 && string(raw[off+16:off+20]) != "MOBI") {
							continue
						}
						crypto := binary.BigEndian.Uint16(raw[off+12:])
						// libmobi src/util.c:mobi_is_encrypted also recognizes
						// DRM v1/v2 on TEXtREAd; those headers need not contain MOBI.
						drm = drm || crypto == 1 || crypto == 2
						if string(raw[off+16:off+20]) == "MOBI" {
							t.Logf("record=%d compression=%d version=%d encoding=%d crypto=%d sourceSHA256=%x", i, binary.BigEndian.Uint16(raw[off:]), binary.BigEndian.Uint32(raw[off+36:]), binary.BigEndian.Uint32(raw[off+28:]), crypto, before)
						} else {
							t.Logf("record=%d compression=%d signature=%q crypto=%d MOBI-fields=unavailable sourceSHA256=%x", i, binary.BigEndian.Uint16(raw[off:]), string(raw[60:68]), crypto, before)
						}
					}
				}
			}
			if drm || strings.EqualFold(filepath.Ext(original), ".fail") {
				t.Setenv("C1BOOK_READER_CACHE_DIR", t.TempDir())
				_, err := OpenDocument(source)
				if err == nil || (drm && !errors.Is(err, mobiformat.ErrDRM)) {
					t.Fatalf("protected/corrupt sample not rejected correctly: %v", err)
				}
				entries, readErr := os.ReadDir(documentCacheDir())
				if readErr != nil && !os.IsNotExist(readErr) {
					t.Fatal(readErr)
				}
				if len(entries) != 0 {
					t.Fatal("rejected sample left cache files")
				}
				return
			}
			mobiIntegrationRoundTrip(t, source)
		})
	}
}

func TestMOBIExportQAFixtures(t *testing.T) {
	destination := os.Getenv("MOBI_QA_EXPORT_DIR")
	if destination == "" {
		t.Skip("set MOBI_QA_EXPORT_DIR to a new directory for self-authored QA books")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.Stat(filepath.Dir(destination))
	if err != nil || !parent.IsDir() {
		t.Fatalf("export parent must already exist: %v", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("export directory must be new; refusing overwrite: %s (%v)", destination, err)
	}
	var first, second strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&first, "清晨读书第%03d段：窗外微风吹过树梢，甲乙两位读者坐在桌旁，逐行检查中文显示、标点和翻页位置。<br/>", i)
		fmt.Fprintf(&second, "夜读记录第%03d段：翻过一页又一页，星光落在安静的书桌上。退出以后重新打开，书签和阅读进度应该仍然指向这里。<br/>", i)
	}
	html := []byte("<html><head><title>中文阅读验收</title></head><body><h1>第一章 清晨阅读</h1><p>" + first.String() + "</p><h1>第二章 夜读与书签</h1><p>" + second.String() + "</p></body></html>")
	legacy := func(compression uint16) []byte {
		records := mobiIntegrationTextRecords(html, compression)
		return mobiIntegrationDatabase(append([][]byte{mobiIntegrationHeader(6, 65001, compression, len(html), len(records))}, records...)...)
	}
	books := []struct {
		name string
		data []byte
	}{
		{"01-未压缩中文.mobi", legacy(1)},
		{"02-PalmDOC中文.mobi", legacy(2)},
		{"03-KF8重组中文.azw3", mobiIntegrationKF8Bodies(false, first.String(), second.String())},
		{"04-混合双格式中文.mobi", mobiIntegrationKF8Bodies(true, first.String(), second.String())},
	}
	t.Setenv("C1BOOK_READER_CACHE_DIR", t.TempDir())
	for _, book := range books {
		path := mobiIntegrationWrite(t, t.TempDir(), book.name, book.data)
		doc, err := OpenDocument(path)
		if err != nil {
			t.Fatalf("validate %s: %v", book.name, err)
		}
		if len(doc.Chapters) != 2 {
			t.Fatalf("%s: expected two selectable chapters, got %d", book.name, len(doc.Chapters))
		}
		for i, chapter := range doc.Chapters {
			pages, end, err := Paginate(doc, chapter, chapter.Start, fixedFace{}, 30, 100)
			if err != nil || end != chapter.End || len(pages) < 5 {
				t.Fatalf("%s chapter %d is not multi-page: pages=%d err=%v", book.name, i+1, len(pages), err)
			}
			t.Logf("QA %s chapter=%d title=%q pages(30cols,10lines)=%d", book.name, i+1, chapter.Title, len(pages))
		}
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	for _, book := range books {
		path := filepath.Join(destination, book.name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(book.data)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("export %s: write=%v close=%v", path, writeErr, closeErr)
		}
		t.Logf("exported=%s sourceBytes=%d SHA256=%x", path, len(book.data), sha256.Sum256(book.data))
	}
}

func mobiDeviceMemory() string {
	if runtime.GOOS != "linux" {
		return "VmRSS=n/a VmHWM=n/a (non-Linux host)"
	}
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return fmt.Sprintf("VmRSS=n/a VmHWM=n/a (%v)", err)
	}
	defer file.Close()
	var fields []string
	scanner := bufio.NewScanner(io.LimitReader(file, 16<<10))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") || strings.HasPrefix(line, "VmHWM:") {
			fields = append(fields, strings.Join(strings.Fields(line), " "))
		}
	}
	if scanner.Err() != nil || len(fields) != 2 {
		return fmt.Sprintf("VmRSS/VmHWM unavailable: %v", scanner.Err())
	}
	return strings.Join(fields, " ")
}

// Cap each diagnostic pagination call at 64 KiB, even for a multi-megabyte
// chapter. Accumulate only a digest and counts, never the book or all its pages.
func mobiDevicePaginate(t *testing.T, doc *Document) (int, [32]byte) {
	t.Helper()
	digest := sha256.New()
	count := 0
	for chapterIndex, chapter := range doc.Chapters {
		for start := chapter.Start; start < chapter.End; {
			raw, end, err := doc.ReadRange(start, chapter.End, 64<<10)
			if err != nil {
				t.Fatal(err)
			}
			if end < chapter.End {
				if at := bytes.LastIndexByte(raw, '\n'); at >= 0 {
					end = start + int64(at+1)
				} else {
					end = start + int64(len(completeTextWindow(raw, doc.Encoding)))
				}
			}
			if end <= start {
				t.Fatalf("diagnostic window did not advance at %d", start)
			}
			window := chapter
			window.End = end
			pages, loadedEnd, err := Paginate(doc, window, start, fixedFace{}, 30, 100)
			if err != nil || loadedEnd != end || len(pages) == 0 {
				t.Fatalf("chapter %d at %d: end=%d err=%v", chapterIndex, start, loadedEnd, err)
			}
			for _, page := range pages {
				if page.Start < start || page.End > end || page.End < page.Start {
					t.Fatal("diagnostic page escaped its bounded window")
				}
				fmt.Fprintf(digest, "%d:%d:%d\n", chapterIndex, page.Start, page.End)
				for _, line := range page.Lines {
					if !utf8.ValidString(line) {
						t.Fatal("diagnostic page contains invalid UTF-8")
					}
					fmt.Fprintf(digest, "%d:%s\n", len(line), line)
				}
			}
			count += len(pages)
			start = end
		}
	}
	var sum [32]byte
	copy(sum[:], digest.Sum(nil))
	return count, sum
}

func TestMOBIDeviceDiagnostics(t *testing.T) {
	root := os.Getenv("MOBI_DEVICE_BOOKS")
	if root == "" {
		t.Skip("set MOBI_DEVICE_BOOKS for read-only, bounded-memory book diagnostics")
	}
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ext := strings.ToLower(filepath.Ext(path))
		if entry.Type().IsRegular() && (ext == ".mobi" || ext == ".azw3") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil || len(paths) == 0 {
		t.Fatalf("device sample discovery: count=%d err=%v", len(paths), err)
	}
	for _, source := range paths {
		name, err := filepath.Rel(root, source)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("C1BOOK_READER_CACHE_DIR", t.TempDir())
			before := fileDigest(t, source)
			mark, err := fingerprint(source)
			if err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			t.Logf("stage=before sourceBytes=%d %s", mark.Size, mobiDeviceMemory())
			started := time.Now()
			doc, err := OpenDocument(source)
			if err != nil {
				t.Fatal(err)
			}
			if doc.Path != source || doc.Encoding != EncodingUTF8 || !validChapterIndex(doc.Chapters, doc.Size) {
				t.Fatal("invalid diagnostic document identity or index")
			}
			t.Logf("stage=cold-open sourceBytes=%d textBytes=%d chapters=%d elapsed=%s %s", mark.Size, doc.Size, len(doc.Chapters), time.Since(started), mobiDeviceMemory())
			started = time.Now()
			pages, sum := mobiDevicePaginate(t, doc)
			t.Logf("stage=paginate pages=%d elapsed=%s %s", pages, time.Since(started), mobiDeviceMemory())
			started = time.Now()
			again, err := OpenDocument(source)
			if err != nil {
				t.Fatal(err)
			}
			if doc.Size != again.Size || !reflect.DeepEqual(doc.Chapters, again.Chapters) || doc.contentPath() != again.contentPath() {
				t.Fatal("cached diagnostic reopen changed text identity/index")
			}
			t.Logf("stage=cache-reopen elapsed=%s %s", time.Since(started), mobiDeviceMemory())
			started = time.Now()
			againPages, againSum := mobiDevicePaginate(t, again)
			if pages == 0 || pages != againPages || sum != againSum {
				t.Fatal("cached diagnostic pagination changed")
			}
			afterMark, err := fingerprint(source)
			if err != nil || mark != afterMark || before != fileDigest(t, source) {
				t.Fatalf("diagnostics changed source: %v", err)
			}
			t.Logf("stage=verified sourceBytes=%d textBytes=%d chapters=%d pages=%d elapsed=%s sourceSHA256=%x pageSHA256=%x %s", mark.Size, doc.Size, len(doc.Chapters), pages, time.Since(started), before, sum, mobiDeviceMemory())
		})
	}
}
