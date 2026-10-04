package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"c1device"
)

func TestScanLibraryDirectChildrenFoldersFirst(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"z.txt", filepath.Join("nested", "B.TXT"), "a.txt", "skip.md"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("text"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt.name"), []byte("蛊真人.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	books, err := ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(books))
	for index := range books {
		got[index] = books[index].Name
	}
	want := []string{"nested", "z.txt", "蛊真人.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %#v, want %#v", got, want)
	}
	if !books[0].IsDir || books[1].IsDir || books[2].Path != filepath.Join(root, "a.txt") {
		t.Fatalf("entry type or original book identity lost: %+v", books)
	}
}

func libraryTestFile(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("第一章 测试\n正文内容。\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLibraryFoldersFormatsAndEmpty(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"z.MOBI", "A.azw3", "c.EPUB", "b.TXT", "ignore.azw", "nested/hidden.txt"} {
		libraryTestFile(t, root, name)
	}
	if err := os.Mkdir(filepath.Join(root, "empty.txt"), 0700); err != nil {
		t.Fatal(err)
	}
	books, err := ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"[目录] empty.txt", "[目录] nested", "A.azw3", "b.TXT", "c.EPUB", "z.MOBI"}
	if got := bookNames(books); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	empty, err := ScanLibrary(filepath.Join(root, "empty.txt"))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty folder = %+v, %v", empty, err)
	}
}

func newLibraryTestApp(t *testing.T, root string) *readerApp {
	t.Helper()
	ui, err := newReaderFace(false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ui.Close() })
	body, err := newReaderFace(true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { body.Close() })
	state := t.TempDir()
	app, err := newReaderApp(root, ui, body, ProgressStore{Dir: state}, BookmarkStore{Dir: state})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestLibraryFolderNavigationAndReaderReturn(t *testing.T) {
	root := t.TempDir()
	libraryTestFile(t, root, "A/first.txt")
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt", "f.txt"} {
		libraryTestFile(t, root, filepath.Join("Z", name))
	}
	if err := os.Mkdir(filepath.Join(root, "Z", "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	app := newLibraryTestApp(t, root)
	app.handle(c1device.KeyDown)
	app.handle(c1device.KeyRight)
	if app.libraryDir != filepath.Join(root, "Z") || app.view != viewShelf {
		t.Fatalf("did not enter Z: %s, %v", app.libraryDir, app.view)
	}
	app.handle(c1device.KeyOK)
	if len(app.books) != 0 || app.shelfLocation() != "/Z/empty" {
		t.Fatalf("empty folder navigation failed: %s, %+v", app.shelfLocation(), app.books)
	}
	app.handle(c1device.KeyOK)
	if app.handle(c1device.KeyBack) || app.libraryDir != filepath.Join(root, "Z") {
		t.Fatal("empty folder back should not exit")
	}
	for i := 0; i < 6; i++ {
		app.handle(c1device.KeyDown)
	}
	before := app.render()
	selected := app.bookIndex
	path := app.books[selected].Path
	app.handle(c1device.KeyOK)
	if app.view != viewChapters || app.document.Path != path {
		t.Fatalf("book did not open with original path: %s", app.message)
	}
	app.handle(c1device.KeyRight)
	if app.view != viewReader {
		t.Fatalf("reader not opened: %s", app.message)
	}
	if err := app.saveProgress(); err != nil {
		t.Fatal(err)
	}
	app.handle(c1device.KeyBack)
	app.handle(c1device.KeyLeft)
	if app.view != viewShelf || app.bookIndex != selected || app.libraryDir != filepath.Join(root, "Z") {
		t.Fatal("reader return lost folder or selection")
	}
	if after := app.render(); !reflect.DeepEqual(before, after) {
		t.Fatal("reader return changed shelf scroll/render")
	}
	if saved, ok, err := app.store.Load(path); err != nil || !ok || saved.Path != path {
		t.Fatalf("original-path progress unavailable: %+v %v %v", saved, ok, err)
	}
	if app.handle(c1device.KeyLeft) || app.libraryDir != root || app.bookIndex != 1 {
		t.Fatal("parent selection not restored")
	}
	app.handle(c1device.KeyOK)
	if app.bookIndex != selected {
		t.Fatal("re-entering folder lost selection")
	}
	app.handle(c1device.KeyBack)
	if !app.handle(c1device.KeyBack) || app.libraryDir != root || app.shelfLocation() != "/" {
		t.Fatal("root Back should exit without ascending")
	}
	if !app.handle(c1device.KeyLeft) {
		t.Fatal("root Left should exit")
	}
}

func TestLibraryNestedBookResumesExistingProgress(t *testing.T) {
	root := t.TempDir()
	path := libraryTestFile(t, root, "folder/book.txt")
	app := newLibraryTestApp(t, root)
	mark, err := fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	offset := int64(len("第一章 测试\n"))
	if err := app.store.Save(Progress{Path: path, Fingerprint: mark, Chapter: 0, Offset: offset, LayoutVersion: 3}); err != nil {
		t.Fatal(err)
	}
	if err := app.bookmarkStore.Save(path, []Bookmark{{Chapter: 0, Offset: offset}}); err != nil {
		t.Fatal(err)
	}
	app.handle(c1device.KeyRight)
	app.handle(c1device.KeyOK)
	if app.document == nil || app.document.Path != path || app.resumeOffset != offset || len(app.bookmarks) != 1 || app.bookmarks[0].Offset != offset {
		t.Fatalf("nested original-path progress/bookmark not restored: offset=%d bookmarks=%v message=%s", app.resumeOffset, app.bookmarks, app.message)
	}
}

func TestLibraryRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	libraryTestFile(t, outside, "outside.txt")
	app := newLibraryTestApp(t, root)
	for _, path := range []string{outside, filepath.Join(root, ".."), root + "-sibling"} {
		if app.changeLibraryDir(path) || app.libraryDir != root {
			t.Fatalf("escaped root via %q", path)
		}
	}
}

func TestLibrarySkipsSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	libraryTestFile(t, outside, "outside.txt")
	libraryTestFile(t, outside, "child/inside.txt")
	app := newLibraryTestApp(t, root)
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable on host: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.txt"), filepath.Join(root, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	books, err := ScanLibrary(root)
	if err != nil || len(books) != 0 {
		t.Fatalf("symlinks should be omitted: %+v %v", books, err)
	}
	if _, err := ScanLibrary(link); err == nil {
		t.Fatal("scanned symlink directory")
	}
	if app.changeLibraryDir(link) {
		t.Fatal("entered symlink directory")
	}
	if app.changeLibraryDir(filepath.Join(link, "child")) {
		t.Fatal("entered directory below symlink")
	}
	for _, path := range []string{filepath.Join(root, "linked.txt"), filepath.Join(link, "child", "inside.txt")} {
		app.books = []Book{{Path: path, Name: "stale.txt"}}
		app.handle(c1device.KeyOK)
		if app.document != nil || app.message == "" {
			t.Fatal("stale entry opened through a symlink")
		}
	}
}

func TestLibraryRestoresSelectionByPathAfterDirectoryChanges(t *testing.T) {
	root := t.TempDir()
	libraryTestFile(t, root, "B/book.txt")
	app := newLibraryTestApp(t, root)
	app.handle(c1device.KeyRight)
	libraryTestFile(t, root, "A/book.txt")
	app.handle(c1device.KeyLeft)
	if app.bookIndex != 1 || app.books[app.bookIndex].Name != "B" {
		t.Fatal("new earlier folder should not steal restored selection")
	}
}

func TestLibraryLocationRenderingAndRotatedNavigation(t *testing.T) {
	root := t.TempDir()
	libraryTestFile(t, root, "folder/book.txt")
	app := newLibraryTestApp(t, root)
	app.lab = &labState{}
	for _, rotation := range []c1device.Orientation{c1device.Rotate0, c1device.Rotate90, c1device.Rotate180, c1device.Rotate270} {
		app.lab.settings.Rotate = rotation
		app.view = viewShelf
		app.libraryDir = root
		app.books, _ = ScanLibrary(root)
		app.bookIndex = 0
		var right, left c1device.Key
		for _, key := range []c1device.Key{c1device.KeyUp, c1device.KeyDown, c1device.KeyLeft, c1device.KeyRight} {
			switch rotation.RemapKey(key) {
			case c1device.KeyRight:
				right = key
			case c1device.KeyLeft:
				left = key
			}
		}
		app.handle(right)
		if app.libraryDir != filepath.Join(root, "folder") {
			t.Fatalf("rotation %v did not enter folder", rotation)
		}
		if frame := app.render(); frame == (c1device.Frame{}) {
			t.Fatalf("rotation %v produced blank shelf", rotation)
		}
		if app.handle(left) || app.libraryDir != root {
			t.Fatalf("rotation %v did not return to root", rotation)
		}
	}
	longPath := "/很长很长的上层目录/另一个很长的目录/当前目录"
	for _, width := range []int{140, 284} {
		label := fitShelfLocation(app.uiFace, longPath, width)
		if app.uiFace.Measure(label) > width || !strings.HasSuffix(label, "当前目录") {
			t.Fatalf("location lost current folder or overflowed: %q", label)
		}
	}
}

func TestLibraryRefusesBookOutsideRoot(t *testing.T) {
	app := newLibraryTestApp(t, t.TempDir())
	path := libraryTestFile(t, t.TempDir(), "outside.txt")
	app.books = []Book{{Path: path, Name: "outside.txt"}}
	app.handle(c1device.KeyOK)
	if app.document != nil || app.view != viewShelf || app.message == "" {
		t.Fatal("opening a stale/outside entry must stay inside the library")
	}
}

func TestLibraryFailedEntryPreservesShelf(t *testing.T) {
	root := t.TempDir()
	path := libraryTestFile(t, root, "gone/book.txt")
	app := newLibraryTestApp(t, root)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	app.handle(c1device.KeyRight)
	if app.libraryDir != root || app.view != viewShelf || app.message == "" || len(app.books) != 1 {
		t.Fatal("failed folder entry should retain shelf and show error")
	}
}
