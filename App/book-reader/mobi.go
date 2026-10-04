package main

import (
	"fmt"
	"io"
	"os"

	"c1book-reader/internal/mobiformat"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

func openMOBIDocument(path string, source *os.File, mark BookFingerprint) (*Document, error) {
	if err := os.MkdirAll(documentCacheDir(), 0700); err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp(documentCacheDir(), ".mobi-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	var text *mobiTextWriter
	err = writePrivateCache(normalizedDocumentPath(path, mark), func(out io.Writer) error {
		text = newMOBITextWriter(out)
		err := mobiformat.Extract(source, mark.Size, temporary, func(title string, encoding uint32, body io.Reader) error {
			switch encoding {
			case 65001:
			case 1252:
				body = transform.NewReader(body, charmap.Windows1252.NewDecoder())
			default:
				return fmt.Errorf("unsupported MOBI/AZW3 text encoding: %d", encoding)
			}
			return text.appendHTML(title, body)
		})
		if err != nil {
			return err
		}
		return text.finish()
	})
	if err != nil {
		return nil, err
	}
	return finishNormalizedDocument(path, mark, text.size, text.chapters)
}
