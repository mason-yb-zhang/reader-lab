package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"c1device"
)

func drawLabFrame(platform c1device.Platform, app *readerApp, full bool) error {
	frame := app.render()
	if err := platform.Draw(frame, full); err != nil {
		return err
	}
	if os.Getenv("C1_LAB_CAPTURE") != "1" {
		return nil
	}
	if err := writeLabCaptureFile(filepath.Join(readerHome(), "last-frame.bin"), frame[:]); err != nil {
		return err
	}
	offset := int64(0)
	if bookmark, ok := app.currentBookmark(); ok {
		offset = bookmark.Offset
	}
	selectedPath := ""
	if app.bookIndex >= 0 && app.bookIndex < len(app.books) {
		selectedPath = app.books[app.bookIndex].Path
	}
	data, err := json.Marshal(struct {
		View         viewMode `json:"view"`
		Fullscreen   bool     `json:"fullscreen"`
		Rotate       uint8    `json:"rotate"`
		Offset       int64    `json:"offset"`
		LineHeight   int      `json:"lineHeight"`
		Message      string   `json:"message"`
		LibraryDir   string   `json:"libraryDir"`
		SelectedPath string   `json:"selectedPath"`
	}{app.view, app.fullscreen, uint8(app.orientation()), offset, app.bodyFace.LineHeight(), app.message, app.libraryDir, selectedPath})
	if err != nil {
		return err
	}
	return writeLabCaptureFile(filepath.Join(readerHome(), "last-state.json"), data)
}

func writeLabCaptureFile(path string, data []byte) error {
	if err := c1device.RequireStoragePath(path); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".lab-capture-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
