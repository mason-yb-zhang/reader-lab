package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

func TestLabMappedFontLifecycle(t *testing.T) {
	app := labFixture(t)
	settings := app.lab.settings
	settings.Font = "Go.ttf"
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	first := app.lab.fontData
	if first == nil || len(first.data) != len(goregular.TTF) || first.typeface != app.lab.typeface {
		t.Fatal("custom font has no owned backing data")
	}
	before, _ := app.currentBookmark()
	settings.Size = 24
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	if app.lab.fontData != first || first.data == nil {
		t.Fatal("changing size replaced or released the backing font")
	}
	if err := os.WriteFile(filepath.Join(app.lab.fontsDir, "Next.ttf"), goregular.TTF, 0600); err != nil {
		t.Fatal(err)
	}
	settings.Font = "Next.ttf"
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	second := app.lab.fontData
	if second == first || first.data != nil || first.typeface != nil || second.data == nil {
		t.Fatal("replacing a font did not release only the old backing data")
	}
	if got, _ := app.currentBookmark(); got != before || !app.chapterPagesMatch() || app.bodyFace.Measure("ABC") == 0 {
		t.Fatal("replacement left a stale page cache, offset or face")
	}
	settings.Font = "fusion12"
	settings.Size = 12
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	if second.data != nil || app.lab.fontData != nil || app.lab.typeface != nil {
		t.Fatal("switching to bitmap retained the custom backing data")
	}
	settings.Font = "Go.ttf"
	if err := app.applyLabSettings(settings, true); err != nil {
		t.Fatal(err)
	}
	last := app.lab.fontData
	original := app.lab.originalFace
	app.closeLab()
	app.closeLab()
	if last.data != nil || last.typeface != nil || app.chapterPagination != nil || app.bodyFace != original || original.Measure("正文") == 0 {
		t.Fatal("closeLab did not detach the page cache and preserve the original face")
	}
	if err := last.Close(); err != nil {
		t.Fatal("repeated close:", err)
	}
}

func TestLabMappedFontFailedTransitionsKeepBackingData(t *testing.T) {
	for _, failure := range []string{"parse", "paginate", "save", "size-save"} {
		t.Run(failure, func(t *testing.T) {
			app := labFixture(t)
			settings := app.lab.settings
			settings.Font = "Go.ttf"
			if err := app.applyLabSettings(settings, true); err != nil {
				t.Fatal(err)
			}
			mapped, face, cache := app.lab.fontData, app.bodyFace, app.chapterPagination
			position, _ := app.currentBookmark()
			oldSettings := app.lab.settings
			data := goregular.TTF
			if failure == "parse" {
				data = []byte("broken font")
			}
			if err := os.WriteFile(filepath.Join(app.lab.fontsDir, "Next.ttf"), data, 0600); err != nil {
				t.Fatal(err)
			}
			settings.Font = "Next.ttf"
			switch failure {
			case "paginate":
				if err := os.Remove(app.document.Path); err != nil {
					t.Fatal(err)
				}
			case "save":
				app.lab.home = app.document.Path
			case "size-save":
				settings.Font, settings.Size = "Go.ttf", 24
				app.lab.home = app.document.Path
			}
			if err := app.applyLabSettings(settings, true); err == nil {
				t.Fatal("expected transition failure")
			}
			if app.lab.fontData != mapped || mapped.data == nil || app.bodyFace != face || app.chapterPagination != cache || app.lab.settings != oldSettings {
				t.Fatal("failed transition released or replaced the old font")
			}
			if got, _ := app.currentBookmark(); got != position || app.bodyFace.Measure("still readable") <= 0 {
				t.Fatal("failed transition invalidated current text or offset")
			}
			app.render()
		})
	}
}

func TestLabMappedFontRejectsInvalidFiles(t *testing.T) {
	root := t.TempDir()
	for _, kind := range []string{"empty", "oversize", "invalid", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(root, kind+".ttf")
			if kind == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if kind == "symlink" {
				target := filepath.Join(root, "target.ttf")
				if err := os.WriteFile(target, goregular.TTF, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skip("host cannot create symlinks:", err)
				}
			} else {
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "oversize" {
					err = file.Truncate(maxLabFontBytes + 1)
				} else if kind == "invalid" {
					_, err = file.WriteString("not a font")
				}
				file.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			mapped, err := openLabMappedFont(path)
			if err == nil || mapped != nil {
				mapped.Close()
				t.Fatal("invalid font source accepted")
			}
		})
	}
}
