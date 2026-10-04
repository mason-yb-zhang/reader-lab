package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"c1device"
)

func (store ProgressStore) Recent(root string) ([]Book, error) {
	if err := c1device.RequireStoragePath(store.Dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(store.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type entry struct {
		book    Book
		updated time.Time
	}
	var recent []entry
	for _, file := range entries {
		if !file.Type().IsRegular() || len(file.Name()) != 29 || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		path := filepath.Join(store.Dir, file.Name())
		stream, err := os.Open(path)
		if err != nil {
			continue
		}
		var saved Progress
		err = json.NewDecoder(io.LimitReader(stream, 8192)).Decode(&saved)
		stream.Close()
		if err != nil || saved.Path == "" || saved.LayoutVersion != layoutVersion || store.progressPath(saved.Path) != path {
			continue
		}
		ext := strings.ToLower(filepath.Ext(saved.Path))
		if ext != ".txt" && ext != ".epub" && ext != ".mobi" && ext != ".azw3" {
			continue
		}
		if checkLibraryBook(root, saved.Path) != nil {
			continue
		}
		info, err := file.Info()
		if err != nil {
			continue
		}
		recent = append(recent, entry{Book{Path: saved.Path, Name: bookDisplayName(saved.Path, filepath.Base(saved.Path))}, info.ModTime()})
	}
	sort.Slice(recent, func(i, j int) bool {
		if recent[i].updated.Equal(recent[j].updated) {
			return recent[i].book.Path < recent[j].book.Path
		}
		return recent[i].updated.After(recent[j].updated)
	})
	if len(recent) > 20 {
		recent = recent[:20]
	}
	books := make([]Book, len(recent))
	for i, e := range recent {
		books[i] = e.book
	}
	return books, nil
}

func (app *readerApp) openRecent() {
	if err := app.saveProgress(); err != nil {
		app.message = err.Error()
		return
	}
	books, err := app.store.Recent(app.libraryRoot)
	if err != nil {
		app.message = err.Error()
		return
	}
	app.recentBooks = books
	app.recentIndex = 0
	app.view = viewRecent
}

func (app *readerApp) openRecentBook() {
	if app.recentIndex < 0 || app.recentIndex >= len(app.recentBooks) {
		return
	}
	app.openBook(app.recentBooks[app.recentIndex].Path)
	if app.view != viewChapters {
		return
	}
	app.fromRecent = true
	app.openChapter(app.chapterIndex, app.resumeOffset)
}
