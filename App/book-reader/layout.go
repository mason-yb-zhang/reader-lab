package main

import "c1device"

// pageLayout is the logical drawing surface for one orientation. Landscape
// values are the historical constants and must stay byte-compatible.
type pageLayout struct {
	orientation      c1device.Orientation
	width, height    int
	headerBottom     int
	bodyTop          int
	bodyBottom       int
	footerTop        int
	textWidth        int
	bodyHeight       int
	fullscreenTop    int
	fullscreenHeight int
	listRowHeight    int
	listFooterTop    int
}

func labLayoutFor(orientation c1device.Orientation) pageLayout {
	width, height := orientation.LogicalSize()
	if orientation.Portrait() {
		bodyBottom := height - 24
		footerTop := height - 22
		return pageLayout{
			orientation:      orientation,
			width:            width,
			height:           height,
			headerBottom:     24,
			bodyTop:          26,
			bodyBottom:       bodyBottom,
			footerTop:        footerTop,
			textWidth:        width - 14,
			bodyHeight:       bodyBottom - 26,
			fullscreenTop:    4,
			fullscreenHeight: height - 8,
			listRowHeight:    22,
			listFooterTop:    footerTop,
		}
	}
	return pageLayout{
		orientation:      orientation,
		width:            c1device.DisplayWidth,
		height:           c1device.DisplayHeight,
		headerBottom:     readerHeaderBottom,
		bodyTop:          readerBodyTop,
		bodyBottom:       readerBodyBottom,
		footerTop:        readerFooterTop,
		textWidth:        readerTextWidth,
		bodyHeight:       readerBodyHeight,
		fullscreenTop:    fullscreenBodyTop,
		fullscreenHeight: fullscreenBodyHeight,
		listRowHeight:    readerListRowHeight,
		listFooterTop:    readerListFooterTop,
	}
}

func (app *readerApp) orientation() c1device.Orientation {
	if app.lab != nil && app.lab.settings.Rotate.Valid() {
		return app.lab.settings.Rotate
	}
	return c1device.Rotate0
}

func (app *readerApp) pageLayout() pageLayout {
	return labLayoutFor(app.orientation())
}
