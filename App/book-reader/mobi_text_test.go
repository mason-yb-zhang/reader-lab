package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

func TestMOBIHTMLStreaming(t *testing.T) {
	var out bytes.Buffer
	text := newMOBITextWriter(&out)
	raw := `<html><head><title>不要重复</title><style>隐藏CSS</style></head><body><h1>第一章 &amp; 开始</h1><p>你好<b>世界</b>。 &copy; A&nbsp;B</p><script>隐藏脚本</script><svg><text>隐藏图片</text></svg><h2>第二章</h2><p>下一段<br>换行</p></body></html>`
	if err := text.appendHTML("测试书", iotest.OneByteReader(strings.NewReader(raw))); err != nil {
		t.Fatal(err)
	}
	if err := text.finish(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"第一章 & 开始", "你好世界。 © A B", "第二章", "下一段\n换行"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %q", want, got)
		}
	}
	for _, unwanted := range []string{"隐藏", "不要重复", "<h1>"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("leaked markup: %q", got)
		}
	}
	if !utf8.ValidString(got) || !validChapterIndex(text.chapters, text.size) {
		t.Fatal("invalid text/index")
	}
	if len(text.chapters) != 2 || text.chapters[0].Title != "第一章 & 开始" || text.chapters[1].Title != "第二章" {
		t.Fatalf("chapters: %+v", text.chapters)
	}
}

func TestMOBIHTMLImplicitHeadEnd(t *testing.T) {
	var out bytes.Buffer
	text := newMOBITextWriter(&out)
	if err := text.appendHTML("书", strings.NewReader("<html><head><title>隐藏标题</title><body><p>可见正文</p></body></html>")); err != nil {
		t.Fatal(err)
	}
	if err := text.finish(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "可见正文\n" {
		t.Fatalf("implicit head closing lost text: %q", out.String())
	}
}

func TestMOBIHTMLPreformattedLineBreaks(t *testing.T) {
	var out bytes.Buffer
	text := newMOBITextWriter(&out)
	if err := text.appendHTML("PalmDOC", strings.NewReader("<pre>第一行\n第二行\n第三行</pre>")); err != nil {
		t.Fatal(err)
	}
	if err := text.finish(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "第一行\n第二行\n第三行\n" {
		t.Fatalf("preformatted paragraphs merged: %q", out.String())
	}
}

func TestMOBIHTMLPartsAndPreamble(t *testing.T) {
	var out bytes.Buffer
	text := newMOBITextWriter(&out)
	for _, s := range []struct{ title, raw string }{{"前言", "<p>前言正文</p><h1>开始</h1><p>正文一</p>"}, {"附录", "<html><body><p>正文二</p></body></html>"}} {
		if err := text.appendHTML(s.title, strings.NewReader(s.raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err := text.finish(); err != nil {
		t.Fatal(err)
	}
	if !validChapterIndex(text.chapters, text.size) || len(text.chapters) != 3 {
		t.Fatalf("%+v", text.chapters)
	}
	if text.chapters[0].Title != "前言" || text.chapters[2].Title != "附录" {
		t.Fatalf("%+v", text.chapters)
	}
}

func TestMOBIHTMLLimitsAndEmpty(t *testing.T) {
	for _, raw := range []string{"<head><title>只有标题</title></head>", "<p>" + strings.Repeat("x", maxMOBIHTMLTokenBytes+1) + "</p>", "<p>\xff</p>"} {
		text := newMOBITextWriter(io.Discard)
		err := text.appendHTML("", strings.NewReader(raw))
		if err == nil {
			err = text.finish()
		}
		if err == nil {
			t.Fatal("invalid or oversized HTML accepted")
		}
	}
}

func TestMOBIHTMLLongHeadingIsBounded(t *testing.T) {
	var out bytes.Buffer
	text := newMOBITextWriter(&out)
	if err := text.appendHTML("", strings.NewReader("<h1>"+strings.Repeat("章", 2000)+"</h1><p>内容</p>")); err != nil {
		t.Fatal(err)
	}
	if err := text.finish(); err != nil {
		t.Fatal(err)
	}
	if len(text.chapters[0].Title) > maxMOBIHeadingBytes || !utf8.ValidString(text.chapters[0].Title) {
		t.Fatal("heading is unbounded or invalid")
	}
	if !strings.Contains(out.String(), strings.Repeat("章", 2000)) {
		t.Fatal("bounded heading must not truncate body")
	}
}

type mobiFailWriter struct{}

func (mobiFailWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestMOBIHTMLPropagatesWriteFailure(t *testing.T) {
	text := newMOBITextWriter(mobiFailWriter{})
	err := text.appendHTML("", strings.NewReader("<p>正文</p>"))
	if err == nil {
		err = text.finish()
	}
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("%v", err)
	}
}
