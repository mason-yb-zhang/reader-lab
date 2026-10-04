package mobiformat

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Hashes come from KindleUnpack bf0ca6e MobiHeader.getRawML and
// K8Processor.buildParts, before link rewriting. They verify byte-for-byte
// reconstruction, not just the presence of recognizable words in fragments.
func TestKindleUnpackOracle(t *testing.T) {
	samples, split := os.Getenv("MOBI_TEST_SAMPLES"), os.Getenv("MOBI_TEST_SPLIT")
	if samples == "" {
		t.Skip("set MOBI_TEST_SAMPLES for upstream oracle checks")
	}
	tests := []struct {
		root, name string
		hashes     []string
	}{
		{samples, "sample-cp1252.mobi", []string{"3f53f73fb33aca66668256097ec195b1c89a3c250a1eeec45534cd65a26a37b6"}},
		{samples, "sample-unicode-huffdic.mobi", []string{"4811c6e4bac8d51c610f945c69a9d0fa9c4988023ecd102ecabe071271b5ae33", "327f4d9d948ae8500e71a53eb1da5f1e73a190d69f5e0c0502c4e32b45cf7460"}},
		{samples, "sample-unicode-uncompressed.mobi", []string{"cf8756295c0b98c502fc56d0ffcf9b8c3ae4a88577b3c7632bfc768bfd749c09", "327f4d9d948ae8500e71a53eb1da5f1e73a190d69f5e0c0502c4e32b45cf7460"}},
		{split, "mobi8-sample-unicode-huffdic.azw3", []string{"4811c6e4bac8d51c610f945c69a9d0fa9c4988023ecd102ecabe071271b5ae33", "327f4d9d948ae8500e71a53eb1da5f1e73a190d69f5e0c0502c4e32b45cf7460"}},
		{split, "mobi7-sample-unicode-huffdic.mobi", []string{"5b71c8e745d6a9d0e7d2df6913722dee4d02b88eddc985122365598b0fb9c003"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.root == "" {
				t.Skip("set MOBI_TEST_SPLIT to KindleUnpack -s output directory")
			}
			f, err := os.Open(filepath.Join(tt.root, tt.name))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			st, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			part := 0
			err = Extract(f, st.Size(), t.TempDir(), func(_ string, _ uint32, r io.Reader) error {
				digest := sha256.New()
				if _, e := io.Copy(digest, r); e != nil {
					return e
				}
				actual := fmt.Sprintf("%x", digest.Sum(nil))
				if part >= len(tt.hashes) || actual != tt.hashes[part] {
					t.Fatalf("part %d mismatch: %s", part, actual)
				}
				part++
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if part != len(tt.hashes) {
				t.Fatalf("part count %d", part)
			}
		})
	}
}
