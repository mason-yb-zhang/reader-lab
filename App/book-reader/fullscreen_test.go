package main

import (
	"reflect"
	"strings"
	"testing"

	"c1device"
)

func TestFullscreenRoundTripPreservesOffsetAndCanonicalPages(t *testing.T) {
	app := labFixture(t)
	normal := append([]int64(nil), app.chapterPagination.starts...)
	for _, offset := range []int64{normal[0], normal[3], normal[8] + 3, normal[len(normal)-1]} {
		if !app.openChapter(0, offset) {
			t.Fatal(app.message)
		}
		for _, want := range []bool{true, false} {
			app.handle(c1device.KeyOK)
			if app.fullscreen != want || app.message != "" {
				t.Fatalf("fullscreen=%v message=%s", app.fullscreen, app.message)
			}
			if got, _ := app.currentBookmark(); got.Offset != offset || app.resumeOffset != offset || app.pageIndex != 0 || !app.chapterPagesMatch() {
				t.Fatal("fullscreen lost exact byte offset or active layout")
			}
		}
		if !reflect.DeepEqual(normal, app.chapterPagination.starts) {
			t.Fatal("normal canonical page boundaries changed")
		}
	}
}

func TestFullscreenCacheDimensionsAndNavigation(t *testing.T) {
	app := chapterPagesFixture(t, strings.Repeat("abcdefghijklmnopqrstuvwxyz\n", 35))
	if err := app.initLab(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.closeLab)
	if !app.openChapter(0, 0) {
		t.Fatal(app.message)
	}
	normal := app.chapterPagination
	if normal.width != readerTextWidth || normal.height != 102 || len(normal.starts) != 7 || len(app.pages[0].Lines) != 5 {
		t.Fatal("normal layout changed")
	}
	app.handle(c1device.KeyOK)
	full := app.chapterPagination
	if !app.fullscreen || full == normal || full.height != 144 || len(full.starts) != 5 {
		t.Fatal("fullscreen used stale dimensions")
	}
	if app.bodyFace.LineHeight() != 20 || fullscreenBodyTop != 4 || len(app.pages[0].Lines) != 7 {
		t.Fatal("fullscreen margins/rows changed")
	}
	if !app.openChapter(0, full.starts[2]+3) || app.chapterPagination != full {
		t.Fatal("same layout rebuilt cache")
	}
	app.nextPage()
	if app.pages[app.pageIndex].Start != full.starts[3] {
		t.Fatal("partial page shifted following page")
	}
	app.previousPage()
	app.previousPage()
	if app.pages[app.pageIndex].Start != full.starts[1] {
		t.Fatal("backward navigation used wrong boundaries")
	}
	for _, dimension := range []string{"width", "height"} {
		stale := *full
		if dimension == "width" {
			stale.width--
		} else {
			stale.height--
		}
		app.chapterPagination = &stale
		if app.chapterPagesMatch() || app.chapterPageLabel() != "页 —/—" {
			t.Fatal("stale dimension matched cache")
		}
		if !app.openChapter(0, full.starts[1]) || app.chapterPagination == &stale {
			t.Fatal("stale dimension reused")
		}
	}
}

func TestFullscreenActionsAndRepeats(t *testing.T) {
	app := labFixture(t)
	for _, event := range []c1device.Event{{Key: c1device.KeyOK}, {Key: c1device.KeyRune, Rune: 'f'}, {Key: c1device.KeyRune, Rune: 'F'}} {
		for _, want := range []bool{true, false} {
			app.handleEvent(event)
			if app.fullscreen != want {
				t.Fatal("fullscreen key did not toggle")
			}
			before := *app
			event.Repeat = true
			app.handleEvent(event)
			event.Repeat = false
			if !reflect.DeepEqual(*app, before) {
				t.Fatal("action repeat toggled fullscreen")
			}
		}
	}
	for _, view := range []viewMode{viewShelf, viewChapters, viewBookmarks, viewPercentJump, viewLabSettings} {
		app.view = view
		app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 'f'})
		if app.fullscreen {
			t.Fatal("F toggled outside reader")
		}
	}
	app.view = viewReader
	app.toggleFullscreen()
	app.handleEvent(c1device.Event{Key: c1device.KeyDown, Repeat: true})
	if app.pageIndex != 1 || !app.fullscreen {
		t.Fatal("fullscreen suppressed navigation repeat")
	}
}

func TestFullscreenPixelsHideChromeAndPreserveMargins(t *testing.T) {
	app := labFixture(t)
	app.handle(c1device.KeyOK)
	frame := app.render()
	expected := c1device.NewCanvas()
	for i, line := range app.pages[0].Lines {
		expected.DrawText(app.bodyFace, 7, 4+i*20, line)
	}
	if frame != expected.Frame(128) {
		t.Fatal("fullscreen contains chrome or misplaced body text")
	}
	black := 0
	for y := 0; y < c1device.DisplayHeight; y++ {
		for x := 0; x < c1device.DisplayWidth; x++ {
			if frameBlack(frame, x, y) {
				black++
				if x < 7 || x >= c1device.DisplayWidth-7 || y < 4 || y >= 148 {
					t.Fatalf("pixel outside body: %d,%d", x, y)
				}
			}
		}
	}
	if black == 0 {
		t.Fatal("fullscreen is blank")
	}
	writeReaderPreview(t, "lab-fullscreen-16", frame)
}

func TestFullscreenCrossChapterAndWindowNavigation(t *testing.T) {
	line := strings.Repeat("a", 31) + "\n"
	app := chapterPagesFixture(t, "第一章 前章\n"+strings.Repeat(line, int(maxChapterWindow)/len(line)+37)+"第二章 后章\n"+strings.Repeat("abcdefgh\n", 22))
	if err := app.initLab(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.closeLab)
	if !app.openChapter(0, maxChapterWindow+32*12+3) {
		t.Fatal(app.message)
	}
	before, _ := app.currentBookmark()
	app.toggleFullscreen()
	if got, _ := app.currentBookmark(); got != before || !app.fullscreen {
		t.Fatal("multi-window toggle lost offset")
	}
	index := app.chapterPagination
	boundary := index.windows[1].firstPage
	if !app.openChapter(0, index.starts[boundary-1]) {
		t.Fatal(app.message)
	}
	app.nextPage()
	if app.pages[app.pageIndex].Start != index.starts[boundary] {
		t.Fatal("forward paging skipped window boundary")
	}
	app.previousPage()
	if app.pages[app.pageIndex].Start != index.starts[boundary-1] {
		t.Fatal("backward paging skipped window boundary")
	}
	if !app.openChapter(1, app.document.Chapters[1].Start) {
		t.Fatal(app.message)
	}
	app.previousPage()
	if app.chapterIndex != 0 || !app.fullscreen || app.chapterPagination.height != 144 {
		t.Fatal("previous chapter used normal layout")
	}
	app.nextPage()
	if app.chapterIndex != 1 || !app.fullscreen || app.chapterPagination.height != 144 {
		t.Fatal("next chapter used normal layout")
	}
}
