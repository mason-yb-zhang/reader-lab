package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"c1device"
	"golang.org/x/image/font/gofont/goregular"
)

func labFixture(t *testing.T) *readerApp {
	t.Helper()
	app := chapterPagesFixture(t, "第一章 阅读实验\n"+strings.Repeat("甲乙丙丁 ABC abc 0123 测试正文。\n", 101))
	readerUIFaces(t, app)
	home, fonts := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(fonts, "Go.ttf"), goregular.TTF, 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.initLab(home, fonts); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.closeLab)
	if !app.openChapter(0, 0) {
		t.Fatal(app.message)
	}
	app.message = ""
	return app
}

func TestLabNewReaderStartupAndShelfTitle(t *testing.T) {
	ui, err := newReaderFace(false)
	if err != nil {
		t.Fatal(err)
	}
	defer ui.Close()
	body, err := newReaderFace(true)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	home := t.TempDir()
	app, err := newReaderApp(t.TempDir(), ui, body, ProgressStore{Dir: home}, BookmarkStore{Dir: home})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.initLab(home, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer app.closeLab()
	if app.view != viewShelf || app.bodyFace != body || app.lab.settings != defaultLabSettings() {
		t.Fatal("lab startup changed the initial shelf or original body face")
	}
	expected := c1device.NewCanvas()
	app.renderList(expected, "墨页实验室", nil, 0, "项", app.shelfHint())
	if app.render() != expected.Frame(128) {
		t.Fatal("lab shelf title was not installed")
	}
	writeReaderPreview(t, "lab-shelf", app.render())
}

func TestLabThresholdClampsAndSettingRepeats(t *testing.T) {
	app := labFixture(t)
	app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 's', Repeat: true})
	if app.view != viewReader {
		t.Fatal("repeated S entered settings")
	}
	app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 's'})
	app.handleEvent(c1device.Event{Key: c1device.KeyDown, Repeat: true})
	app.handleEvent(c1device.Event{Key: c1device.KeyDown, Repeat: true})
	if app.lab.selected != 2 {
		t.Fatal("navigation repeats did not select threshold")
	}
	for i := 0; i < 20; i++ {
		app.handle(c1device.KeyRight)
	}
	if app.lab.settings.Threshold != 224 {
		t.Fatal("threshold exceeded upper bound")
	}
	for i := 0; i < 20; i++ {
		app.handle(c1device.KeyLeft)
	}
	if app.lab.settings.Threshold != 32 || app.view != viewLabSettings {
		t.Fatal("left adjustment exited settings or crossed lower bound")
	}
}

func TestLabDefaultLayoutAndOptIn(t *testing.T) {
	app := chapterPagesFixture(t, strings.Repeat("abcdefghijklmnopqrstuvwxyz\n", 35))
	readerUIFaces(t, app)
	if !app.openChapter(0, 0) {
		t.Fatal(app.message)
	}
	before := app.render()
	face, cache := app.bodyFace, app.chapterPagination
	for _, event := range []c1device.Event{{Key: c1device.KeyOK}, {Key: c1device.KeyRune, Rune: 'f'}, {Key: c1device.KeyRune, Rune: 's'}} {
		app.handleEvent(event)
	}
	if app.lab != nil || app.fullscreen || app.view != viewReader || app.bodyFace != face || app.chapterPagination != cache {
		t.Fatal("lab actions changed a non-lab reader")
	}
	if err := app.initLab(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.closeLab)
	if app.bodyFace != face || app.chapterPagination != cache || app.bodyFace.LineHeight() != 20 || app.lab.settings != defaultLabSettings() {
		t.Fatal("initialization changed the native 16px layout")
	}
	got := app.render()
	for y := 0; y < readerFooterTop; y++ {
		for x := 0; x < c1device.DisplayWidth; x++ {
			if frameBlack(got, x, y) != frameBlack(before, x, y) {
				t.Fatal("default header/body pixels changed")
			}
		}
	}
	if app.uiFace.Measure(app.readerHint()) > c1device.DisplayWidth {
		t.Fatal("lab shortcut hint overflows")
	}
	if layoutVersion != 3 {
		t.Fatal("layoutVersion changed")
	}
}

func TestLabSettingsKeyboardNavigation(t *testing.T) {
	app := labFixture(t)
	app.nextPage()
	before, _ := app.currentBookmark()
	app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 'S'})
	if app.view != viewLabSettings {
		t.Fatal("S did not open settings")
	}
	app.handle(c1device.KeyRight)
	if app.lab.settings.Font != "fusion12" || app.lab.settings.Size != 12 || app.view != viewLabSettings {
		t.Fatal("font row did not select native Fusion12")
	}
	app.handle(c1device.KeyDown)
	app.handle(c1device.KeyRight)
	if app.lab.settings.Size != 12 || app.message == "" {
		t.Fatal("native font must reject a different size")
	}
	app.handle(c1device.KeyDown)
	app.handle(c1device.KeyRight)
	if app.lab.settings.Threshold != defaultLabSettings().Threshold+16 {
		t.Fatal("threshold did not step by 16")
	}
	app.handle(c1device.KeyDown)
	app.handle(c1device.KeyRight)
	if !app.fullscreen || app.view != viewLabSettings {
		t.Fatal("settings fullscreen toggle left settings")
	}
	app.handle(c1device.KeyDown)
	if app.lab.selected != 4 {
		t.Fatal("selection did not land on rotate row")
	}
	app.handle(c1device.KeyDown)
	if app.lab.selected != 4 {
		t.Fatal("selection escaped settings rows")
	}
	app.handle(c1device.KeyRight)
	if app.lab.settings.Rotate != c1device.Rotate90 || !app.orientation().Portrait() {
		t.Fatal("rotate row did not enter portrait")
	}
	app.handle(c1device.KeyBack)
	if got, _ := app.currentBookmark(); app.view != viewReader || got != before {
		t.Fatalf("Back did not preserve reading position: %v %v", got, before)
	}
	for _, view := range []viewMode{viewShelf, viewChapters, viewBookmarks, viewPercentJump} {
		app.view = view
		app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 's'})
		if app.view != view {
			t.Fatal("S opened settings outside the reader")
		}
	}
}

func TestLabBadFontDoesNotBlockLaterFonts(t *testing.T) {
	app := labFixture(t)
	if err := os.WriteFile(filepath.Join(app.lab.fontsDir, "Bad.otf"), []byte("not a font"), 0600); err != nil {
		t.Fatal(err)
	}
	fonts, err := scanLabFonts(app.lab.fontsDir)
	if err != nil {
		t.Fatal(err)
	}
	app.lab.fonts = fonts
	app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 's'})
	app.handle(c1device.KeyRight)
	before, _ := app.currentBookmark()
	app.handle(c1device.KeyRight)
	if app.lab.settings.Font != "fusion12" || app.message == "" {
		t.Fatal("bad font did not preserve the working font")
	}
	app.handle(c1device.KeyRight)
	if got, _ := app.currentBookmark(); app.lab.settings.Font != "Go.ttf" || got != before || app.message != "" {
		t.Fatal("a broken font blocked navigation to the next usable font")
	}
}

func TestLabSettingsPersistAndReopen(t *testing.T) {
	app := labFixture(t)
	settings := labSettings{Font: "Go.ttf", Size: 24, Threshold: 224, Fullscreen: true}
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	reopened := readingFixture(t)
	original := reopened.bodyFace
	if err := reopened.initLab(app.lab.home, app.lab.fontsDir); err != nil {
		t.Fatal(err)
	}
	if reopened.lab.settings != settings || !reopened.fullscreen || reopened.lab.typeface == nil {
		t.Fatal("reopening did not load persisted settings")
	}
	reopened.closeLab()
	reopened.closeLab()
	if reopened.bodyFace != original || original.Measure("原始字体") <= 0 {
		t.Fatal("closing lab invalidated the original main-owned face")
	}
}

func TestLabInvalidConfigurationFailsClosed(t *testing.T) {
	for _, data := range []string{
		`null`, `{}`, `{"font":"bitmap","size":15,"threshold":48}`,
		`{"font":"fusion12","size":16,"threshold":48}`,
		`{"font":"bitmap","size":16,"threshold":31}`,
		`{"font":"bitmap","size":16,"threshold":225}`,
		`{"font":"../Go.ttf","size":16,"threshold":48}`,
		`{"font":"C:\\Go.ttf","size":16,"threshold":48}`,
		`{"font":"bitmap","size":16,"threshold":48,"unknown":true}`,
		`{"font":"bitmap","size":16,"threshold":48} {}`,
		strings.Repeat("x", 4097),
	} {
		t.Run(fmt.Sprintf("%q", data[:min(len(data), 80)]), func(t *testing.T) {
			app := readingFixture(t)
			before := *app
			home := t.TempDir()
			path := filepath.Join(home, "reader-settings.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := app.initLab(home, t.TempDir()); err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if !reflect.DeepEqual(*app, before) {
				t.Fatal("invalid configuration changed reader state")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != data {
				t.Fatal("invalid configuration was overwritten")
			}
		})
	}
}

func TestLabFontSizeAndFullscreenPreserveOffset(t *testing.T) {
	app := labFixture(t)
	offset := app.chapterPagination.starts[4] + 3
	if !app.openChapter(0, offset) {
		t.Fatal(app.message)
	}
	app.readerOrigin = viewBookmarks
	for _, font := range []string{"bitmap", "fusion12", "Go.ttf", "bitmap"} {
		for _, size := range labFontSizes {
			if font == "fusion12" && size != 12 {
				continue
			}
			for _, full := range []bool{true, false} {
				settings := labSettings{Font: font, Size: size, Threshold: 48, Fullscreen: full}
				if err := app.applyLabSettings(settings, true); err != nil {
					t.Fatalf("%s %d %v: %v", font, size, full, err)
				}
				if got, _ := app.currentBookmark(); got.Offset != offset || app.resumeOffset != offset || app.readerOrigin != viewBookmarks || !app.chapterPagesMatch() {
					t.Fatalf("%s %d %v lost offset/layout: %+v", font, size, full, got)
				}
			}
		}
	}
	if err := app.saveProgress(); err != nil {
		t.Fatal(err)
	}
	saved, ok, err := app.store.Load(app.document.Path)
	if err != nil || !ok || saved.Offset != offset || saved.LayoutVersion != 3 {
		t.Fatalf("progress format or offset changed: %+v %v", saved, err)
	}
}

func TestLabCustomTypefaceReusedAcrossSizes(t *testing.T) {
	app := labFixture(t)
	settings := app.lab.settings
	settings.Font = "Go.ttf"
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	parsed := app.lab.typeface
	if err := os.Remove(filepath.Join(app.lab.fontsDir, "Go.ttf")); err != nil {
		t.Fatal(err)
	}
	for _, size := range labFontSizes {
		settings.Size = size
		if err := app.applyLabSettings(settings, true); err != nil || app.lab.typeface != parsed {
			t.Fatalf("size %d reread/reparsed the current font: %v", size, err)
		}
	}
	settings.Font = "bitmap"
	if err := app.applyLabSettings(settings, true); err != nil || app.lab.typeface != nil {
		t.Fatal("leaving custom font retained its parsed bytes")
	}
}

func TestLabFailedTransitionsKeepOldSettingsAndPosition(t *testing.T) {
	for _, failure := range []string{"font", "count", "read", "save"} {
		t.Run(failure, func(t *testing.T) {
			app := labFixture(t)
			app.nextPage()
			settings := app.lab.settings
			path := filepath.Join(app.lab.home, "reader-settings.json")
			persisted, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "font":
				settings.Font = "missing.ttf"
			case "count":
				settings.Size = 18
				if err := os.Remove(app.document.Path); err != nil {
					t.Fatal(err)
				}
			case "read":
				settings.Fullscreen = true
				index, err := app.countedChapterForLayout(app.chapterIndex, fullscreenBodyHeight)
				if err != nil {
					t.Fatal(err)
				}
				app.chapterPagination = index
				if err := os.Remove(app.document.Path); err != nil {
					t.Fatal(err)
				}
			case "save":
				settings.Size = 20
				app.lab.home = app.document.Path
			}
			before, beforeLab := *app, *app.lab
			if err := app.applyLabSettings(settings, true); err == nil {
				t.Fatal("expected failure")
			}
			if !reflect.DeepEqual(*app, before) || !reflect.DeepEqual(*app.lab, beforeLab) {
				t.Fatal("failure changed old settings, face, cache or position")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(persisted) {
				t.Fatal("failed transition changed persisted settings")
			}
		})
	}
}

func TestLabStorageGateAndFontBoundaries(t *testing.T) {
	app := readingFixture(t)
	if c1device.RequireStoragePath("/storage/c1/reader-lab/state") != nil {
		if err := app.initLab("/storage/c1/reader-lab/state", t.TempDir()); err == nil || app.lab != nil {
			t.Fatal("initialization bypassed device storage gate")
		}
	}
	app = labFixture(t)
	app.lab.home = "/storage/c1/reader-lab/state"
	if c1device.RequireStoragePath(app.lab.home) != nil {
		settings := app.lab.settings
		settings.Size = 18
		if err := app.applyLabSettings(settings, true); err == nil || app.lab.settings.Size != 16 {
			t.Fatal("settings save bypassed device storage gate")
		}
	}
	for _, name := range []string{"../escape.ttf", `..\escape.otf`, "/absolute.ttf", "test.txt", "bad\n.ttf", "test.ttf:stream"} {
		if validLabFontName(name) {
			t.Fatalf("unsafe font name accepted: %q", name)
		}
	}
	oversize := filepath.Join(app.lab.fontsDir, "large.ttf")
	file, err := os.Create(oversize)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxLabFontBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	settings := app.lab.settings
	settings.Font = "large.ttf"
	if _, _, err := app.lab.newFace(settings); err == nil {
		t.Fatal("oversize font accepted")
	}
	fonts, err := scanLabFonts(app.lab.fontsDir)
	if err != nil || reflect.DeepEqual(fonts, []string{"bitmap", "fusion12"}) || len(fonts) != 3 {
		t.Fatalf("font list should include Go.ttf and exclude oversize font: %v %v", fonts, err)
	}
}

func TestLabSettingsPixelsUseUIFaceAndBitmapIgnoresThreshold(t *testing.T) {
	app := labFixture(t)
	app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 's'})
	before := app.render()
	writeReaderPreview(t, "lab-settings-default", before)
	settings := app.lab.settings
	settings.Size = 24
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	after := app.render()
	// Only the size row changes; all other UI text remains native 16px.
	for y := 0; y < c1device.DisplayHeight; y++ {
		if y >= 48 && y < 70 {
			continue
		}
		for x := 0; x < c1device.DisplayWidth; x++ {
			if frameBlack(before, x, y) != frameBlack(after, x, y) {
				t.Fatalf("body face affected settings chrome at %d,%d", x, y)
			}
		}
	}
	app.handle(c1device.KeyBack)
	for _, font := range []string{"bitmap", "fusion12"} {
		settings.Font, settings.Size, settings.Threshold = font, 12, 32
		if err := app.applyLabSettings(settings, true); err != nil {
			t.Fatal(err)
		}
		before := app.render()
		settings.Threshold = 224
		if err := app.applyLabSettings(settings, true); err != nil {
			t.Fatal(err)
		}
		if app.render() != before {
			t.Fatalf("threshold changed %s bitmap pixels", font)
		}
		writeReaderPreview(t, "lab-"+font+"-12", before)
	}
	settings.Font, settings.Size = "Go.ttf", 24
	settings.Threshold = 32
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	light := app.render()
	settings.Threshold = 224
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	if light == app.render() {
		t.Fatal("threshold had no effect on an outline font")
	}
	writeReaderPreview(t, "lab-outline-24", app.render())
}
