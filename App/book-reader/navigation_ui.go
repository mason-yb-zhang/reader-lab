package main

import (
	"c1device"
	"image"
	"strings"
)

func fitShelfLocation(face *c1device.Face, path string, width int) string {
	if face.Measure(path) <= width {
		return path
	}
	runes := []rune(path)
	for len(runes) > 0 && face.Measure("…"+string(runes)) > width {
		runes = runes[1:]
	}
	return "…" + string(runes)
}

// Four independent cues in physical direction order: left, up, down, right.
// Commands such as P bookmarks/delete live in the header instead.
func (app *readerApp) drawNavigationFooter(canvas *c1device.Canvas, hint string) {
	labels := strings.Fields(hint)
	if len(labels) != 4 {
		return
	}
	layout := app.pageLayout()
	width := canvas.Width()
	// Historical landscape positions; portrait spreads the same four cues evenly.
	landscapeX := [4]int{4, 82, 158, 244}
	slot := (width - 8) / 4
	for i, label := range labels {
		x := 4 + i*slot
		if !layout.orientation.Portrait() {
			x = landscapeX[i]
		}
		limit := 70
		if layout.orientation.Portrait() {
			limit = slot - 2
		}
		canvas.DrawText(app.uiFace, x, layout.listFooterTop+3, fitText(app.uiFace, label, limit))
	}
	canvas.InvertRect(image.Rect(0, layout.listFooterTop, width, canvas.Height()))
}
