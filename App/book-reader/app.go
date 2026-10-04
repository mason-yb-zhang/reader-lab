package main

import (
	"fmt"
	"image"
	"path/filepath"

	"c1device"
)

type viewMode uint8

const (
	viewShelf viewMode = iota
	viewChapters
	viewBookmarks
	viewReader
	viewPercentJump
	viewLabSettings
	viewRecent
)

const (
	readerHeaderBottom   = 24
	readerBodyTop        = 26
	readerBodyBottom     = 128
	readerFooterTop      = 132
	readerTextWidth      = c1device.DisplayWidth - 14
	readerBodyHeight     = readerBodyBottom - readerBodyTop
	fullscreenBodyTop    = 4
	fullscreenBodyHeight = c1device.DisplayHeight - 2*fullscreenBodyTop
	readerListRowHeight  = 22
	readerListFooterTop  = 130
	chapterListHint      = "←返回  ↑上移  ↓下移  →阅读"
	bookmarkListHint     = "←返回  ↑上移  ↓下移  →跳转"
	readerFooterPrefix   = "←章节  ↑上页  ↓下页  "
)

type readerApp struct {
	libraryRoot       string
	libraryDir        string
	shelfSelections   map[string]shelfSelection
	books             []Book
	recentBooks       []Book
	recentIndex       int
	fromRecent        bool
	bookIndex         int
	chapterIndex      int
	chapterPick       int
	bookmarkPick      int
	view              viewMode
	readerOrigin      viewMode
	document          *Document
	pages             []Page
	chapterPagination *chapterPages
	pageIndex         int
	windowStart       int64
	windowEnd         int64
	resumeOffset      int64
	uiFace            *c1device.Face
	bodyFace          *c1device.Face
	store             ProgressStore
	bookmarkStore     BookmarkStore
	bookmarks         []Bookmark
	dirty             bool
	message           string
	percentInput      string
	percentValue      int
	percentOrigin     viewMode
	expandedVolumes   map[int]bool
	volumeBodyPick    bool
	fullscreen        bool
	lab               *labState
}

func newReaderApp(booksDir string, uiFace, bodyFace *c1device.Face, store ProgressStore, bookmarkStore BookmarkStore) (*readerApp, error) {
	books, err := ScanLibrary(booksDir)
	if err != nil {
		return nil, err
	}
	return &readerApp{
		libraryRoot: filepath.Clean(booksDir), libraryDir: filepath.Clean(booksDir),
		books: books, uiFace: uiFace, bodyFace: bodyFace,
		store: store, bookmarkStore: bookmarkStore, view: viewShelf,
	}, nil
}

func (app *readerApp) handle(key c1device.Key) (exit bool) {
	return app.handleEvent(c1device.Event{Key: key})
}

func (app *readerApp) handleEvent(event c1device.Event) (exit bool) {
	// Only navigation repeats. Holding an action key must not toggle a
	// bookmark twice or carry an open/back action into the next view.
	if event.Repeat {
		switch event.Key {
		case c1device.KeyUp, c1device.KeyDown, c1device.KeyVolumeUp, c1device.KeyVolumeDown:
		default:
			return false
		}
	}
	// Visual arrows follow the rotated screen; volume keys stay physical page turns.
	event.Key = app.orientation().RemapKey(event.Key)
	// Use volume + for down and volume - for up, as requested for navigation.
	// The numeric jump dialog retains +/- values.
	if app.view != viewPercentJump {
		switch event.Key {
		case c1device.KeyVolumeUp:
			event.Key = c1device.KeyDown
		case c1device.KeyVolumeDown:
			event.Key = c1device.KeyUp
		}
	}
	app.message = ""
	switch app.view {
	case viewShelf:
		switch event.Key {
		case c1device.KeyUp:
			app.bookIndex = moveSelection(app.bookIndex, -1, len(app.books))
		case c1device.KeyDown:
			app.bookIndex = moveSelection(app.bookIndex, 1, len(app.books))
		case c1device.KeyOK, c1device.KeyRight:
			app.openSelectedBook()
		case c1device.KeyBack, c1device.KeyLeft:
			return app.leaveLibraryFolder()
		case c1device.KeyRune:
			if event.Rune == 'r' || event.Rune == 'R' {
				app.openRecent()
			}
		}
	case viewRecent:
		switch event.Key {
		case c1device.KeyUp:
			app.recentIndex = moveSelection(app.recentIndex, -1, len(app.recentBooks))
		case c1device.KeyDown:
			app.recentIndex = moveSelection(app.recentIndex, 1, len(app.recentBooks))
		case c1device.KeyOK, c1device.KeyRight:
			app.openRecentBook()
		case c1device.KeyBack, c1device.KeyLeft:
			app.view = viewShelf
			app.fromRecent = false
		}
	case viewChapters:
		switch event.Key {
		case c1device.KeyUp:
			app.moveDirectorySelection(-chapterSelectionDelta(event))
		case c1device.KeyDown:
			app.moveDirectorySelection(chapterSelectionDelta(event))
		case c1device.KeyOK, c1device.KeyRight:
			app.activateDirectorySelection()
		case c1device.KeyRune:
			if event.Rune == 'o' || event.Rune == 'O' {
				app.openPercentJump()
			}
		case c1device.KeyPause:
			app.bookmarkPick = 0
			app.view = viewBookmarks
		case c1device.KeyBack, c1device.KeyLeft:
			app.leaveDirectory()
		}
	case viewBookmarks:
		switch event.Key {
		case c1device.KeyRune:
			if event.Rune == 'o' || event.Rune == 'O' {
				app.openPercentJump()
			}
		case c1device.KeyUp:
			app.bookmarkPick = moveSelection(app.bookmarkPick, -1, len(app.bookmarks))
		case c1device.KeyDown:
			app.bookmarkPick = moveSelection(app.bookmarkPick, 1, len(app.bookmarks))
		case c1device.KeyOK, c1device.KeyRight:
			app.readerOrigin = viewBookmarks
			app.openSelectedBookmark()
		case c1device.KeyPause:
			app.deleteSelectedBookmark()
		case c1device.KeyBack, c1device.KeyLeft:
			app.view = viewChapters
		}
	case viewPercentJump:
		app.handlePercentJump(event)
	case viewLabSettings:
		app.handleLabSettings(event)
	case viewReader:
		switch event.Key {
		case c1device.KeyOK:
			app.toggleFullscreen()
		case c1device.KeyLeft, c1device.KeyBack:
			if bookmark, ok := app.currentBookmark(); ok {
				app.resumeOffset = bookmark.Offset
			}
			app.percentInput = ""
			app.revealChapter(app.chapterIndex)
			if app.readerOrigin == viewBookmarks {
				app.view = viewBookmarks
			} else {
				app.view = viewChapters
			}
		case c1device.KeyUp:
			app.previousPage()
		case c1device.KeyDown:
			app.nextPage()
		case c1device.KeyRight:
			app.toggleBookmark()
		case c1device.KeyRune:
			switch event.Rune {
			case 'o', 'O':
				app.openPercentJump()
			case 'f', 'F':
				app.toggleFullscreen()
			case 's', 'S':
				if app.lab != nil {
					app.view = viewLabSettings
				}
			}
		}
	}
	return false
}

func chapterSelectionDelta(event c1device.Event) int {
	if event.Repeat {
		return 5
	}
	return 1
}

func moveSelection(current, delta, count int) int {
	if count == 0 {
		return 0
	}
	current += delta
	if current < 0 {
		return 0
	}
	if current >= count {
		return count - 1
	}
	return current
}

func (app *readerApp) openSelectedBook() {
	if app.bookIndex < 0 || app.bookIndex >= len(app.books) {
		return
	}
	if app.books[app.bookIndex].IsDir {
		app.changeLibraryDir(app.books[app.bookIndex].Path)
		return
	}
	app.fromRecent = false
	app.openBook(app.books[app.bookIndex].Path)
}

func (app *readerApp) openBook(path string) {
	if app.libraryRoot != "" {
		if err := checkLibraryBook(app.libraryRoot, path); err != nil {
			app.message = err.Error()
			return
		}
	}
	// The delayed save belongs to the old document. Flush it before replacing
	// that document, including when reopening the same book from the shelf.
	if err := app.saveProgress(); err != nil {
		app.message = err.Error()
		return
	}
	document, err := OpenDocument(path)
	if err != nil {
		app.message = err.Error()
		return
	}
	app.document = document
	app.expandedVolumes = nil
	app.volumeBodyPick = false
	app.chapterIndex = 0
	app.chapterPick = 0
	if rows, _ := app.directoryRows(); len(rows) > 0 {
		app.chapterPick = rows[0].chapter
	}
	app.bookmarkPick = 0
	app.pages = nil
	app.chapterPagination = nil
	app.pageIndex = 0
	app.resumeOffset = 0
	app.bookmarks = nil
	if saved, ok, err := app.store.Load(document.Path); err != nil {
		app.message = err.Error()
	} else if ok {
		if chapter, valid := restoredChapter(document, saved.Chapter, saved.Offset); valid {
			app.revealChapter(chapter)
			app.chapterIndex = chapter
			app.resumeOffset = saved.Offset
		}
	}
	bookmarks, err := app.bookmarkStore.Load(document.Path)
	if err != nil {
		app.message = err.Error()
	} else {
		app.bookmarks = validBookmarks(document, bookmarks)
	}
	app.view = viewChapters
}

func validPosition(document *Document, chapterIndex int, offset int64) bool {
	if document == nil || chapterIndex < 0 || chapterIndex >= len(document.Chapters) {
		return false
	}
	chapter := document.Chapters[chapterIndex]
	return offset >= chapter.Start && offset < chapter.End
}

// TXT heading recognition may split an old chapter, while its source bytes
// remain identical. The fingerprint-checked byte offset is authoritative.
func restoredChapter(document *Document, stored int, offset int64) (int, bool) {
	if validPosition(document, stored, offset) {
		return stored, true
	}
	if document == nil || document.dataPath != "" || stored < 0 || stored >= len(document.Chapters) || offset < 0 || offset >= document.Size {
		return 0, false
	}
	for i, chapter := range document.Chapters {
		if offset >= chapter.Start && offset < chapter.End {
			return i, true
		}
	}
	return 0, false
}

func validBookmarks(document *Document, bookmarks []Bookmark) []Bookmark {
	valid := make([]Bookmark, 0, len(bookmarks))
	for _, bookmark := range bookmarks {
		if chapter, ok := restoredChapter(document, bookmark.Chapter, bookmark.Offset); ok {
			bookmark.Chapter = chapter
			valid = append(valid, bookmark)
		}
	}
	return valid
}

func (app *readerApp) openChapter(chapterIndex int, offset int64) bool {
	if app.document == nil || chapterIndex < 0 || chapterIndex >= len(app.document.Chapters) {
		return false
	}
	originalChapter := chapterIndex
	var ok bool
	chapterIndex, offset, ok = app.document.readingTarget(chapterIndex, offset)
	if !ok {
		app.message = "此书只有卷标题，暂无正文"
		return false
	}
	if chapterIndex != originalChapter {
		var err error
		offset, err = app.document.AlignLineStart(offset, app.document.Chapters[chapterIndex].Start)
		if err != nil {
			app.message = err.Error()
			return false
		}
	}
	if !validPosition(app.document, chapterIndex, offset) {
		offset = app.document.Chapters[chapterIndex].Start
	}
	return app.openCountedChapter(chapterIndex, offset, false)
}

func (app *readerApp) loadChapter(offset int64) {
	app.openChapter(app.chapterIndex, offset)
}

func (app *readerApp) nextPage() {
	if app.pageIndex+1 < len(app.pages) {
		app.pageIndex++
		app.dirty = true
		return
	}
	chapter := app.document.Chapters[app.chapterIndex]
	if app.windowEnd < chapter.End {
		app.loadChapter(app.windowEnd)
		return
	}
	if next, ok := app.document.readableChapter(app.chapterIndex+1, 1); ok {
		app.openChapter(next, app.document.Chapters[next].Start)
	}
}

func (app *readerApp) previousPage() {
	if app.pageIndex > 0 {
		app.pageIndex--
		app.dirty = true
		return
	}
	app.previousChapterPage()
}

func (app *readerApp) jumpToPercent(percent int) bool {
	if percent < 0 || percent > 100 {
		return false
	}
	return app.jumpToPercentUnits(percent * percentScale)
}

func (app *readerApp) jumpToPercentUnits(units int) bool {
	chapterIndex, offset, ok := positionForPercentUnits(app.document, units)
	if !ok {
		app.message = "无法跳转：没有可读正文"
		return false
	}
	aligned, err := app.document.AlignLineStart(offset, app.document.Chapters[chapterIndex].Start)
	if err != nil {
		app.message = err.Error()
		return false
	}
	return app.openChapter(chapterIndex, aligned)
}

func positionForPercent(document *Document, percent int) (int, int64, bool) {
	if percent < 0 || percent > 100 {
		return 0, 0, false
	}
	return positionForPercentUnits(document, percent*percentScale)
}

func positionForPercentUnits(document *Document, units int) (int, int64, bool) {
	if document == nil || document.Size <= 0 || units < 0 || units > maxPercentUnits || len(document.Chapters) == 0 {
		return 0, 0, false
	}
	// Quotient/remainder avoids overflowing int64 for very large documents.
	target := document.Size/maxPercentUnits*int64(units) + document.Size%maxPercentUnits*int64(units)/maxPercentUnits
	if target >= document.Size {
		target = document.Size - 1
	}
	if target < document.Chapters[0].Start {
		target = document.Chapters[0].Start
	}
	for index, chapter := range document.Chapters {
		if target >= chapter.Start && target < chapter.End {
			return document.readingTarget(index, target)
		}
	}
	return 0, 0, false
}

func (app *readerApp) currentBookmark() (Bookmark, bool) {
	if app.document == nil || len(app.pages) == 0 || app.pageIndex < 0 || app.pageIndex >= len(app.pages) {
		return Bookmark{}, false
	}
	return Bookmark{Chapter: app.chapterIndex, Offset: app.pages[app.pageIndex].Start}, true
}

func (app *readerApp) readerHint() string {
	action := "→书签"
	if bookmark, ok := app.currentBookmark(); ok {
		for _, saved := range app.bookmarks {
			if saved == bookmark {
				action = "→删除"
				break
			}
		}
	}
	if app.lab != nil {
		return "←返回 ↑↓翻页 " + action + " S设置 F全屏"
	}
	prefix := readerFooterPrefix
	if app.readerOrigin == viewBookmarks {
		prefix = "←书签  ↑上页  ↓下页  "
	}
	return prefix + action
}

func (app *readerApp) readerBodyHeight() int {
	layout := app.pageLayout()
	if app.fullscreen {
		return layout.fullscreenHeight
	}
	return layout.bodyHeight
}

func (app *readerApp) toggleFullscreen() {
	if app.lab == nil || app.view != viewReader {
		return
	}
	bookmark, ok := app.currentBookmark()
	if !ok || !validPosition(app.document, bookmark.Chapter, bookmark.Offset) {
		return
	}
	settings := app.lab.settings
	settings.Fullscreen = !app.fullscreen
	if err := app.applyLabSettings(settings, true); err != nil {
		app.message = err.Error()
	}
}

func (app *readerApp) toggleBookmark() {
	bookmark, ok := app.currentBookmark()
	if !ok {
		return
	}
	for index, saved := range app.bookmarks {
		if saved == bookmark {
			app.bookmarks = append(app.bookmarks[:index], app.bookmarks[index+1:]...)
			app.saveBookmarks()
			return
		}
	}
	app.bookmarks = normalizeBookmarks(append(app.bookmarks, bookmark))
	app.saveBookmarks()
}

func (app *readerApp) openSelectedBookmark() {
	if app.bookmarkPick < 0 || app.bookmarkPick >= len(app.bookmarks) {
		return
	}
	bookmark := app.bookmarks[app.bookmarkPick]
	app.openChapter(bookmark.Chapter, bookmark.Offset)
}

func (app *readerApp) deleteSelectedBookmark() {
	if app.bookmarkPick < 0 || app.bookmarkPick >= len(app.bookmarks) {
		return
	}
	app.bookmarks = append(app.bookmarks[:app.bookmarkPick], app.bookmarks[app.bookmarkPick+1:]...)
	app.bookmarkPick = moveSelection(app.bookmarkPick, 0, len(app.bookmarks))
	app.saveBookmarks()
}

func (app *readerApp) saveBookmarks() {
	if err := app.bookmarkStore.Save(app.document.Path, app.bookmarks); err != nil {
		app.message = err.Error()
	}
}

func (app *readerApp) saveProgress() error {
	if !app.dirty {
		return nil
	}
	progress, ok := app.currentProgress()
	if !ok {
		return fmt.Errorf("cannot read current book progress")
	}
	if err := app.store.Save(progress); err != nil {
		return err
	}
	app.dirty = false
	return nil
}

func (app *readerApp) currentProgress() (Progress, bool) {
	if app.document == nil || app.chapterIndex < 0 || app.chapterIndex >= len(app.document.Chapters) {
		return Progress{}, false
	}
	fingerprint, err := fingerprint(app.document.Path)
	if err != nil {
		return Progress{}, false
	}
	offset := app.document.Chapters[app.chapterIndex].Start
	if len(app.pages) > 0 && app.pageIndex >= 0 && app.pageIndex < len(app.pages) {
		offset = app.pages[app.pageIndex].Start
	} else if validPosition(app.document, app.chapterIndex, app.resumeOffset) {
		offset = app.resumeOffset
	}
	return Progress{Fingerprint: fingerprint, Path: app.document.Path, Chapter: app.chapterIndex, Offset: offset, LayoutVersion: layoutVersion}, true
}

func (app *readerApp) render() c1device.Frame {
	canvas := c1device.NewCanvasOrientation(app.orientation())
	canvas.Clear()
	switch app.view {
	case viewShelf:
		title := "书架"
		if app.lab != nil {
			title = "墨页实验室"
		}
		app.renderList(canvas, title, bookNames(app.books), app.bookIndex, "项", app.shelfHint())
	case viewRecent:
		app.renderList(canvas, "最近阅读", bookNames(app.recentBooks), app.recentIndex, "本", "←书架 ↑上移 ↓下移 →续读")
	case viewChapters:
		app.renderDirectory(canvas)
	case viewBookmarks:
		app.renderList(canvas, "书签", app.bookmarkLabels(), app.bookmarkPick, "个", bookmarkListHint)
	case viewReader:
		app.renderReader(canvas)
	case viewPercentJump:
		app.renderPercentJump(canvas)
	case viewLabSettings:
		app.renderLabSettings(canvas)
	}
	return canvas.Frame(128)
}

func bookNames(books []Book) []string {
	names := make([]string, len(books))
	for index := range books {
		names[index] = books[index].Name
		if books[index].IsDir {
			names[index] = "[目录] " + names[index]
		}
	}
	return names
}

func (app *readerApp) bookmarkLabels() []string {
	labels := make([]string, 0, len(app.bookmarks))
	for _, bookmark := range app.bookmarks {
		if !validPosition(app.document, bookmark.Chapter, bookmark.Offset) {
			continue
		}
		percent := float64(bookmark.Offset) * 100 / float64(app.document.Size)
		labels = append(labels, fmt.Sprintf("%s  %.1f%%", app.document.contextualChapterTitle(bookmark.Chapter), percent))
	}
	return labels
}

func (app *readerApp) renderList(canvas *c1device.Canvas, title string, items []string, selected int, unit, hint string) {
	layout := app.pageLayout()
	rowTop, headerBottom := layout.headerBottom+2, layout.headerBottom
	width := canvas.Width()
	count := fmt.Sprintf("%d %s", len(items), unit)
	if app.view == viewChapters {
		count = app.directoryCount()
	}
	titleWidth := width - 6 - app.uiFace.Measure(count) - 14
	if app.view == viewShelf {
		rowTop, headerBottom = 42, 40
		buttonWidth := 94
		canvas.DrawText(app.uiFace, 6, 19, fitShelfLocation(app.uiFace, app.shelfLocation(), width-buttonWidth-20))
		canvas.DrawTextInverted(app.uiFace, image.Rect(width-buttonWidth-6, 19, width-6, 37), width-buttonWidth-2, 20, "R 最近阅读")
	}
	if app.view == viewChapters || app.view == viewBookmarks {
		rowTop, headerBottom = 40, 40
		buttonWidth := 76
		if width < 200 {
			buttonWidth = width/2 - 10
		}
		canvas.DrawTextInverted(app.uiFace, image.Rect(6, 19, 6+buttonWidth, 37), 12, 20, fitText(app.uiFace, "O跳转", buttonWidth-12))
		shortcut := "P书签"
		if app.view == viewBookmarks {
			shortcut = "P删除"
		}
		left := 6 + buttonWidth + 6
		canvas.DrawTextInverted(app.uiFace, image.Rect(left, 19, left+buttonWidth, 37), left+6, 20, fitText(app.uiFace, shortcut, buttonWidth-12))
	}
	canvas.DrawText(app.uiFace, 6, 1, fitText(app.uiFace, title, titleWidth))
	canvas.DrawTextRight(app.uiFace, width-6, 1, count)
	canvas.FillRect(image.Rect(4, headerBottom-2, width-4, headerBottom))
	if len(items) == 0 {
		emptyTitle, emptyHint := "暂无内容", "请连接设备后添加文件"
		if app.view == viewShelf {
			emptyTitle, emptyHint = "暂无可显示内容", "←返回上层，根目录退出"
		}
		if title == "书签" {
			emptyTitle, emptyHint = "暂无书签", "阅读时按 → 添加书签"
		}
		if app.view == viewRecent {
			emptyTitle, emptyHint = "暂无阅读记录", "开始阅读后自动记录"
		}
		canvas.DrawTextCentered(app.uiFace, width/2, 52, emptyTitle)
		canvas.DrawTextCentered(app.uiFace, width/2, 78, fitText(app.uiFace, emptyHint, width-16))
	} else {
		visible := (layout.listFooterTop - rowTop) / layout.listRowHeight
		start := selected - visible/2
		if start < 0 {
			start = 0
		}
		if start+visible > len(items) {
			start = len(items) - visible
			if start < 0 {
				start = 0
			}
		}
		for index := start; index < len(items) && index < start+visible; index++ {
			top := rowTop + (index-start)*layout.listRowHeight
			prefix := "  "
			if index == selected {
				prefix = "› "
			}
			text := fitText(app.uiFace, prefix+items[index], width-22)
			if index == selected {
				canvas.DrawTextInverted(app.uiFace, image.Rect(5, top, width-10, top+layout.listRowHeight-1), 8, top+1, text)
			} else {
				canvas.DrawText(app.uiFace, 8, top+1, text)
			}
		}
		canvas.DrawScrollIndicator(width-6, rowTop, layout.listFooterTop, selected, len(items))
	}
	footer := hint
	if app.message != "" {
		footer = fitText(app.uiFace, app.message, width-16)
	}
	if app.message == "" && (app.view == viewShelf || app.view == viewChapters || app.view == viewBookmarks || app.view == viewRecent) {
		app.drawNavigationFooter(canvas, hint)
	} else {
		canvas.DrawInvertedTextBar(app.uiFace, image.Rect(0, layout.listFooterTop, width, canvas.Height()), fitText(app.uiFace, footer, width-16))
	}
}

func (app *readerApp) renderReader(canvas *c1device.Canvas) {
	layout := app.pageLayout()
	if app.fullscreen {
		app.renderReaderBody(canvas, layout.fullscreenTop)
		return
	}
	width := canvas.Width()
	percent := 0.0
	if len(app.pages) > 0 && app.document.Size > 0 {
		percent = float64(app.pages[app.pageIndex].Start) * 100 / float64(app.document.Size)
	}
	percentage := fmt.Sprintf("%.2f%%", percent)
	pageLabel := app.chapterPageLabel()
	percentLeft := width - 6 - app.uiFace.Measure(percentage)
	pageLeft := percentLeft - 10 - app.uiFace.Measure(pageLabel)
	canvas.DrawText(app.uiFace, 6, 1, fitText(app.uiFace, app.document.contextualChapterTitle(app.chapterIndex), pageLeft-14))
	canvas.DrawText(app.uiFace, pageLeft, 1, pageLabel)
	canvas.DrawTextRight(app.uiFace, width-6, 1, percentage)
	canvas.FillRect(image.Rect(4, layout.headerBottom-2, width-4, layout.headerBottom))
	app.renderReaderBody(canvas, layout.bodyTop)
	footer := app.readerHint()
	if app.message != "" {
		footer = app.message
	}
	canvas.DrawInvertedTextBar(app.uiFace, image.Rect(0, layout.footerTop, width, canvas.Height()), fitText(app.uiFace, footer, width-16))
}

func (app *readerApp) renderReaderBody(canvas *c1device.Canvas, top int) {
	if len(app.pages) == 0 {
		return
	}
	for _, line := range app.pages[app.pageIndex].Lines {
		if app.lab != nil && app.lab.settings.Font != "bitmap" && app.lab.settings.Font != "fusion12" {
			canvas.DrawTextThreshold(app.bodyFace, 7, top, line, uint8(app.lab.settings.Threshold))
		} else {
			canvas.DrawText(app.bodyFace, 7, top, line)
		}
		top += app.bodyFace.LineHeight()
	}
}

func fitText(face *c1device.Face, text string, width int) string {
	if face.Measure(text) <= width {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && face.Measure(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
