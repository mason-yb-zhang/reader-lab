package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"c1device"
)

func TestRecentUsesExistingProgressAndFilters(t *testing.T) {
	root := t.TempDir()
	store := ProgressStore{Dir: t.TempDir()}
	var want string
	for i := 0; i < 23; i++ {
		path := libraryTestFile(t, root, fmt.Sprintf("folder/%02d.txt", i))
		mark, _ := fingerprint(path)
		if err := store.Save(Progress{Path: path, Fingerprint: mark}); err != nil {
			t.Fatal(err)
		}
		date := time.Unix(1700000000+int64(i), 0)
		if err := os.Chtimes(store.progressPath(path), date, date); err != nil {
			t.Fatal(err)
		}
		want = path
	}
	outside := libraryTestFile(t, t.TempDir(), "outside.txt")
	if err := store.Save(Progress{Path: outside}); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(root, "gone.txt")
	if err := store.Save(Progress{Path: gone}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "reader-settings.json"), []byte(`{"path":"bad"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, strings.Repeat("a", 24)+".json"), []byte("bad json"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Recent(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 || entries[0].Path != want {
		t.Fatalf("recent list: %d %+v", len(entries), entries)
	}
	if entries[0].Name != "22.txt" {
		t.Fatalf("display name: %s", entries[0].Name)
	}
}

func TestRecentEmptyAndDuplicateIdentity(t *testing.T) {
	root := t.TempDir()
	store := ProgressStore{Dir: filepath.Join(t.TempDir(), "missing")}
	entries, err := store.Recent(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("%v %v", entries, err)
	}
	path := libraryTestFile(t, root, "a.txt")
	mark, _ := fingerprint(path)
	progress := Progress{Path: path, Fingerprint: mark}
	if err := store.Save(progress); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(progress)
	if err := os.WriteFile(filepath.Join(store.Dir, strings.Repeat("b", 24)+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	entries, err = store.Recent(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("forged identity duplicated history: %v %v", entries, err)
	}
}

func TestRecentShortcutResumesAndReturnsToFolder(t *testing.T) {
	root := t.TempDir()
	path := libraryTestFile(t, root, "deep/book.txt")
	text := "第一章 测试\n" + strings.Repeat("这是用于测试阅读位置恢复的正文。\n", 100)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	app := newLibraryTestApp(t, root)
	mark, _ := fingerprint(path)
	offset := int64(len("第一章 测试\n"))
	if err := app.store.Save(Progress{Path: path, Fingerprint: mark, Chapter: 0, Offset: offset}); err != nil {
		t.Fatal(err)
	}
	app.handle(c1device.KeyRight)
	folder, selected := app.libraryDir, app.bookIndex
	writeReaderPreview(t, "recent-shelf", app.render())
	app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 'r'})
	if app.view != viewRecent || len(app.recentBooks) != 1 {
		t.Fatalf("recent not opened: %s", app.message)
	}
	writeReaderPreview(t, "recent-list", app.render())
	app.handle(c1device.KeyOK)
	if app.view != viewReader || app.document.Path != path || app.pages[app.pageIndex].Start != offset {
		t.Fatalf("not resumed directly: %d %s", app.view, app.message)
	}
	app.handle(c1device.KeyDown)
	saved := app.pages[app.pageIndex].Start
	app.handle(c1device.KeyBack)
	app.handle(c1device.KeyBack)
	if app.view != viewRecent {
		t.Fatalf("not returned to recent: %d", app.view)
	}
	app.handle(c1device.KeyBack)
	if app.view != viewShelf || app.libraryDir != folder || app.bookIndex != selected {
		t.Fatal("folder context lost")
	}
	app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 'R'})
	app.handle(c1device.KeyRight)
	if app.view != viewReader || app.pages[app.pageIndex].Start != saved {
		t.Fatal("latest page not retained")
	}
}

func TestRecentEmptyBackAndDeletedBook(t *testing.T) {
	root := t.TempDir()
	app := newLibraryTestApp(t, root)
	app.handleEvent(c1device.Event{Key: c1device.KeyRune, Rune: 'r'})
	if app.view != viewRecent || len(app.recentBooks) != 0 {
		t.Fatal("empty recent unavailable")
	}
	writeReaderPreview(t, "recent-empty", app.render())
	app.handle(c1device.KeyOK)
	if app.view != viewRecent || app.handle(c1device.KeyBack) || app.view != viewShelf {
		t.Fatal("empty recent Back exited app")
	}
	path := libraryTestFile(t, root, "gone.txt")
	mark, _ := fingerprint(path)
	if err := app.store.Save(Progress{Path: path, Fingerprint: mark}); err != nil {
		t.Fatal(err)
	}
	app.openRecent()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	app.handle(c1device.KeyOK)
	if app.view != viewRecent || app.message == "" {
		t.Fatal("deleted history item did not report error safely")
	}
}
