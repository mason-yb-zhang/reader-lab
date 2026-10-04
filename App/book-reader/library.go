package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type Book struct {
	Path  string
	Name  string
	IsDir bool
}

func ScanLibrary(root string) ([]Book, error) {
	if err := checkLibraryDir(root, root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	books := make([]Book, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			books = append(books, Book{Path: path, Name: entry.Name(), IsDir: true})
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !entry.Type().IsRegular() || (ext != ".txt" && ext != ".epub" && ext != ".mobi" && ext != ".azw3") {
			continue
		}
		books = append(books, Book{Path: path, Name: bookDisplayName(path, entry.Name())})
	}
	sort.SliceStable(books, func(i, j int) bool {
		if books[i].IsDir != books[j].IsDir {
			return books[i].IsDir
		}
		left, right := strings.ToLower(books[i].Name), strings.ToLower(books[j].Name)
		if left == right {
			return books[i].Path < books[j].Path
		}
		return left < right
	})
	return books, nil
}

func checkLibraryDir(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("目录超出书库范围")
	}
	current := filepath.Clean(root)
	parts := []string{"."}
	if rel != "." {
		parts = append(parts, strings.Split(rel, string(filepath.Separator))...)
	}
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("不是可访问的书库目录")
		}
	}
	return nil
}

func checkLibraryBook(root, path string) error {
	if err := checkLibraryDir(root, filepath.Dir(path)); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("不是可访问的书籍文件")
	}
	return nil
}

type shelfSelection struct {
	path  string
	index int
}

func (app *readerApp) changeLibraryDir(path string) bool {
	path = filepath.Clean(path)
	if err := checkLibraryDir(app.libraryRoot, path); err != nil {
		app.message = err.Error()
		return false
	}
	books, err := ScanLibrary(path)
	if err != nil {
		app.message = err.Error()
		return false
	}
	selection := shelfSelection{index: app.bookIndex}
	if app.bookIndex >= 0 && app.bookIndex < len(app.books) {
		selection.path = app.books[app.bookIndex].Path
	}
	if app.shelfSelections == nil {
		app.shelfSelections = make(map[string]shelfSelection)
	}
	app.shelfSelections[app.libraryDir] = selection
	restore := app.shelfSelections[path]
	app.libraryDir, app.books = path, books
	app.bookIndex = moveSelection(restore.index, 0, len(books))
	for index, book := range books {
		if book.Path == restore.path {
			app.bookIndex = index
			break
		}
	}
	return true
}

func (app *readerApp) leaveLibraryFolder() bool {
	if app.libraryDir == app.libraryRoot {
		return true
	}
	app.changeLibraryDir(filepath.Dir(app.libraryDir))
	return false
}

func (app *readerApp) shelfLocation() string {
	rel, err := filepath.Rel(app.libraryRoot, app.libraryDir)
	if err != nil || rel == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

func (app *readerApp) shelfHint() string {
	back := "←上层"
	if app.libraryDir == app.libraryRoot {
		back = "←退出"
	}
	return back + " ↑上移 ↓下移 →打开"
}

func bookDisplayName(path, fallback string) string {
	data, err := os.ReadFile(path + ".name")
	if err != nil || len(data) == 0 || len(data) > 4096 || !utf8.Valid(data) {
		return fallback
	}
	name := strings.TrimSpace(string(data))
	if name == "" || strings.ContainsAny(name, "\x00\r\n") {
		return fallback
	}
	return name
}
