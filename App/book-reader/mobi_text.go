package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const (
	maxMOBIHTMLTokenBytes = 256 << 10
	maxMOBIHeadingBytes   = 768
	maxMOBIChapters       = 10000
)

type mobiTextWriter struct {
	out         *bufio.Writer
	size        int64
	chapters    []Chapter
	lineStart   bool
	space       bool
	heading     bool
	headingText strings.Builder
}

func newMOBITextWriter(out io.Writer) *mobiTextWriter {
	return &mobiTextWriter{out: bufio.NewWriterSize(out, 32<<10), lineStart: true}
}

func (w *mobiTextWriter) emit(s string) error {
	if int64(len(s)) > maxNormalizedTextBytes-w.size {
		return errors.New("MOBI/AZW3 text exceeds size limit")
	}
	n, err := w.out.WriteString(s)
	w.size += int64(n)
	if err == nil && w.heading && w.headingText.Len()+len(s) <= maxMOBIHeadingBytes {
		w.headingText.WriteString(s)
	}
	return err
}

func (w *mobiTextWriter) newline() error {
	w.space = false
	if w.lineStart {
		return nil
	}
	w.lineStart = true
	return w.emit("\n")
}

func (w *mobiTextWriter) addChapter(title string) error {
	w.finishHeading()
	if err := w.newline(); err != nil {
		return err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "正文"
	}
	title = boundedMOBITitle(title)
	n := len(w.chapters)
	if n > 0 && w.chapters[n-1].Start == w.size {
		w.chapters[n-1].Title = title
		return nil
	}
	if n >= maxMOBIChapters {
		return errors.New("MOBI/AZW3 chapter count exceeds limit")
	}
	if n > 0 {
		w.chapters[n-1].End = w.size
	}
	w.chapters = append(w.chapters, Chapter{Title: title, Start: w.size, HasBody: true})
	return nil
}

func boundedMOBITitle(s string) string {
	if len(s) <= maxMOBIHeadingBytes {
		return s
	}
	end := maxMOBIHeadingBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

func (w *mobiTextWriter) finishHeading() {
	if !w.heading {
		return
	}
	title := strings.TrimSpace(w.headingText.String())
	if title != "" && len(w.chapters) > 0 {
		w.chapters[len(w.chapters)-1].Title = title
	}
	w.heading = false
	w.headingText.Reset()
}

func mobiBlockTag(tag string) bool {
	switch tag {
	case "p", "div", "section", "article", "blockquote", "li", "ul", "ol", "dl", "dt", "dd", "tr", "table", "pre", "br", "hr", "mbp:pagebreak":
		return true
	}
	return false
}

func mobiHeadingTag(tag string) bool {
	return len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'
}

func mobiHiddenTag(tag string) bool {
	switch tag {
	case "head", "script", "style", "svg", "math", "object", "iframe", "template", "noscript":
		return true
	}
	return false
}

func (w *mobiTextWriter) appendHTML(title string, source io.Reader) error {
	if err := w.addChapter(title); err != nil {
		return err
	}
	tokenizer := html.NewTokenizer(source)
	tokenizer.SetMaxBuf(maxMOBIHTMLTokenBytes)
	hidden := ""
	depth := 0
	preformatted := 0
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			if !errors.Is(tokenizer.Err(), io.EOF) {
				return fmt.Errorf("MOBI/AZW3 HTML: %w", tokenizer.Err())
			}
			break
		}
		if kind == html.TextToken {
			if hidden != "" {
				continue
			}
			data := tokenizer.Text()
			if !utf8.Valid(data) {
				return errors.New("invalid UTF-8 in MOBI/AZW3 text")
			}
			for _, r := range string(data) {
				if r == '\uFEFF' || r == '\u00AD' {
					continue
				}
				if preformatted > 0 && (r == '\n' || r == '\r') {
					if err := w.newline(); err != nil {
						return err
					}
					continue
				}
				if unicode.IsSpace(r) {
					w.space = !w.lineStart
					continue
				}
				if w.space && !w.lineStart {
					if err := w.emit(" "); err != nil {
						return err
					}
				}
				w.space = false
				if err := w.emit(string(r)); err != nil {
					return err
				}
				w.lineStart = false
			}
			continue
		}
		if kind != html.StartTagToken && kind != html.EndTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		name, _ := tokenizer.TagName()
		tag := string(name)
		if hidden == "head" && tag == "body" && kind == html.StartTagToken {
			hidden = ""
			depth = 0
		}
		if hidden != "" {
			if tag == hidden {
				if kind == html.StartTagToken {
					depth++
				} else if kind == html.EndTagToken {
					depth--
					if depth == 0 {
						hidden = ""
					}
				}
			}
			continue
		}
		if mobiHiddenTag(tag) {
			if kind == html.StartTagToken {
				hidden = tag
				depth = 1
			}
			continue
		}
		if tag == "pre" {
			if kind == html.StartTagToken {
				preformatted++
			} else if kind == html.EndTagToken && preformatted > 0 {
				preformatted--
			}
		}
		if mobiHeadingTag(tag) {
			if kind == html.StartTagToken {
				if err := w.addChapter(title); err != nil {
					return err
				}
				w.heading = true
			} else {
				w.finishHeading()
				if err := w.newline(); err != nil {
					return err
				}
			}
		} else if mobiBlockTag(tag) {
			w.finishHeading()
			if err := w.newline(); err != nil {
				return err
			}
		} else if tag == "td" || tag == "th" {
			w.space = !w.lineStart
		}
	}
	w.finishHeading()
	return w.newline()
}

func (w *mobiTextWriter) finish() error {
	w.finishHeading()
	if err := w.newline(); err != nil {
		return err
	}
	if w.size == 0 {
		return errors.New("MOBI/AZW3 contains no readable text")
	}
	n := len(w.chapters)
	if n > 1 && w.chapters[n-1].Start == w.size {
		w.chapters = w.chapters[:n-1]
	}
	w.chapters[len(w.chapters)-1].End = w.size
	return w.out.Flush()
}
