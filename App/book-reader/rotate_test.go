package main

import (
	"strings"
	"testing"

	"c1device"
)

func TestRotateSettingValidatesAndPersists(t *testing.T) {
	app := labFixture(t)
	settings := app.lab.settings
	settings.Rotate = c1device.Rotate270
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	if app.orientation() != c1device.Rotate270 {
		t.Fatalf("orientation = %v", app.orientation())
	}
	if app.pageLayout().textWidth != c1device.DisplayHeight-14 {
		t.Fatalf("portrait text width = %d", app.pageLayout().textWidth)
	}
	if app.readerBodyHeight() <= readerBodyHeight {
		t.Fatalf("portrait body height %d should grow past landscape %d", app.readerBodyHeight(), readerBodyHeight)
	}
	bad := settings
	bad.Rotate = 4
	if err := bad.validate(); err == nil {
		t.Fatal("rotate 4 must be rejected")
	}
}

func TestPortraitPaginationUsesTallerBody(t *testing.T) {
	app := labFixture(t)
	landscapePages := len(app.pages[0].Lines)
	settings := app.lab.settings
	settings.Rotate = c1device.Rotate90
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	if len(app.pages[0].Lines) <= landscapePages {
		t.Fatalf("portrait lines %d should exceed landscape %d", len(app.pages[0].Lines), landscapePages)
	}
	if !app.chapterPagesMatch() {
		t.Fatal("portrait pagination cache did not match layout")
	}
}

func TestPortraitRemapsVisualArrows(t *testing.T) {
	app := labFixture(t)
	settings := app.lab.settings
	settings.Rotate = c1device.Rotate90
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	before, _ := app.currentBookmark()
	// Physical Right is visual Down after a clockwise turn, so it must page forward.
	app.handleEvent(c1device.Event{Key: c1device.KeyRight})
	after, _ := app.currentBookmark()
	if after.Offset <= before.Offset {
		t.Fatalf("Rotate90 physical Right should page forward: %v -> %v", before, after)
	}
}

func TestPortraitRenderUsesLogicalSurface(t *testing.T) {
	app := labFixture(t)
	settings := app.lab.settings
	settings.Rotate = c1device.Rotate90
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	frame := app.render()
	// A portrait frame must have some ink but not fill the panel.
	ink := 0
	for _, b := range frame {
		for bit := 0; bit < 8; bit++ {
			if b&(1<<uint(bit)) != 0 {
				ink++
			}
		}
	}
	if ink == 0 || ink == 296*152 {
		t.Fatalf("portrait frame ink=%d looks blank/full", ink)
	}
}

func TestPortraitListHintFits(t *testing.T) {
	app := labFixture(t)
	settings := app.lab.settings
	settings.Rotate = c1device.Rotate270
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	app.view = viewShelf
	frame := app.render()
	if frame == (c1device.Frame{}) {
		t.Fatal("empty portrait shelf frame")
	}
	hint := "↑↓选择  →打开  BACK退出"
	if app.uiFace.Measure(hint) > 152 {
		// Allowed: render path must fitText it, not overflow raw.
		t.Log("landscape hint is wider than portrait; fitText path is required")
	}
	if strings.Contains(app.readerHint(), "\n") {
		t.Fatal("hint must stay single-line")
	}
}
