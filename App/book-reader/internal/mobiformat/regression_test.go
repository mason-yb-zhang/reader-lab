package mobiformat

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

func TestRepeatedFrontInsertionChargesCopyBudget(t *testing.T) {
	parts := []span{{0, 26}}
	work := 0
	for i := 0; i < 19999; i++ {
		next, err := insertSpan(parts, 12, span{int64(26 + i), 1}, &work)
		if errors.Is(err, ErrLimit) {
			if i > 1500 {
				t.Fatalf("copy budget was charged too late: %d insertions", i)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		parts = next
	}
	t.Fatalf("accepted quadratic copying: %d spans, work %d", len(parts), work)
}

func TestPalmDOC300KiBHTMLTokenBoundaries(t *testing.T) {
	body := bytes.Repeat([]byte{'x'}, 300<<10)
	for i := 4095; i+1 < len(body); i += 4096 {
		body[i] = '\r'
		body[i+1] = '\n'
	}
	for i := 128; i+3 < len(body); i += 131 {
		copy(body[i:], []byte{'&', '<', '>', 0x80})
	}
	count := (len(body) + 4095) / 4096
	h := make([]byte, 16)
	binary.BigEndian.PutUint16(h, 1)
	put32(h, 4, uint32(len(body)))
	binary.BigEndian.PutUint16(h[8:], uint16(count))
	records := [][]byte{h}
	for start := 0; start < len(body); start += 4096 {
		records = append(records, body[start:min(start+4096, len(body))])
	}
	b := testDatabase(records...)
	copy(b[60:], "TEXtREAd")
	var text strings.Builder
	err := Extract(bytes.NewReader(b), int64(len(b)), t.TempDir(), func(_ string, enc uint32, r io.Reader) error {
		if enc != 1252 {
			t.Fatalf("unexpected encoding: %d", enc)
		}
		tokenizer := html.NewTokenizer(transform.NewReader(r, charmap.Windows1252.NewDecoder()))
		tokenizer.SetMaxBuf(256 << 10)
		for {
			switch tokenizer.Next() {
			case html.ErrorToken:
				if errors.Is(tokenizer.Err(), io.EOF) {
					return nil
				}
				return tokenizer.Err()
			case html.TextToken:
				text.Write(tokenizer.Text())
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(body)
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.ReplaceAll(strings.ReplaceAll(string(decoded), "\r\n", "\n"), "\r", "\n")
	if text.String() != expected {
		t.Fatalf("visible text/newlines changed: got %d bytes, want %d", text.Len(), len(expected))
	}
}
