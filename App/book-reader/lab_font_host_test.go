//go:build !linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLabHostFontReadHasExactAllocationAndDetectsSizeChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.ttf")
	if err := os.WriteFile(path, []byte("12345678"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{7, 8, 9} {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := loadLabFontData(file, size)
		file.Close()
		if size == 8 {
			if err != nil || len(data) != 8 || cap(data) != 8 {
				t.Fatalf("expected one exact-size backing buffer: len=%d cap=%d err=%v", len(data), cap(data), err)
			}
		} else if err == nil || data != nil {
			t.Fatalf("size %d did not reject a changed file", size)
		}
	}
}
