package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"c1device"
)

//go:embed assets/fusion12.bin
var fusion12Bitmap []byte

const maxLabFontBytes = 16 << 20

var labFontSizes = []int{12, 14, 16, 18, 20, 24}

type labSettings struct {
	Font       string `json:"font"`
	Size       int    `json:"size"`
	Threshold  int    `json:"threshold"`
	Fullscreen bool   `json:"fullscreen"`
}

type labState struct {
	home, fontsDir string
	settings       labSettings
	fonts          []string
	selected       int
	fontPick       int
	originalFace   *c1device.Face
	ownedFace      *c1device.Face
	typeface       *c1device.Typeface
	fontData       *labMappedFont
}

func defaultLabSettings() labSettings {
	return labSettings{Font: "bitmap", Size: 16, Threshold: 128}
}

func validLabFontName(name string) bool {
	if name == "bitmap" || name == "fusion12" {
		return true
	}
	if name == "" || len(name) > 255 || filepath.Base(name) != name || strings.ContainsAny(name, `/\:`) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".ttf" || ext == ".otf"
}

func (settings labSettings) validate() error {
	if !validLabFontName(settings.Font) {
		return fmt.Errorf("无效字体名称")
	}
	allowed := false
	for _, size := range labFontSizes {
		allowed = allowed || settings.Size == size
	}
	if !allowed {
		return fmt.Errorf("字号仅支持12/14/16/18/20/24")
	}
	if settings.Font == "fusion12" && settings.Size != 12 {
		return fmt.Errorf("Fusion原生点阵固定12px")
	}
	if settings.Threshold < 32 || settings.Threshold > 224 {
		return fmt.Errorf("黑白阈值须在32至224之间")
	}
	return nil
}

func (app *readerApp) initLab(home, fontsDir string) error {
	if app.lab != nil {
		return fmt.Errorf("阅读实验室已初始化")
	}
	path := filepath.Join(home, "reader-settings.json")
	for _, destination := range []string{path, fontsDir} {
		if err := c1device.RequireStoragePath(destination); err != nil {
			return err
		}
	}
	settings := defaultLabSettings()
	file, err := os.Open(path)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > 4096 {
			return fmt.Errorf("阅读设置文件过大")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		// Decode into a fresh value so missing numeric fields fail validation.
		var loaded *labSettings
		if err := decoder.Decode(&loaded); err != nil {
			return fmt.Errorf("读取阅读设置: %w", err)
		}
		if loaded == nil {
			return fmt.Errorf("阅读设置不能为空")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return fmt.Errorf("阅读设置包含多余内容")
		}
		settings = *loaded
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := settings.validate(); err != nil {
		return err
	}
	fonts, err := scanLabFonts(fontsDir)
	if err != nil {
		return err
	}
	app.lab = &labState{home: home, fontsDir: fontsDir, fonts: fonts, settings: defaultLabSettings(), originalFace: app.bodyFace}
	if err := app.applyLabSettings(settings, true); err != nil {
		app.lab = nil
		return err
	}
	return nil
}

func scanLabFonts(dir string) ([]string, error) {
	fonts := []string{"bitmap", "fusion12"}
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return fonts, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("字体目录不是普通目录")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !validLabFontName(entry.Name()) || entry.Name() == "bitmap" || entry.Name() == "fusion12" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= maxLabFontBytes {
			fonts = append(fonts, entry.Name())
		}
	}
	return fonts, nil
}

func (lab *labState) newFace(settings labSettings) (*c1device.Face, *labMappedFont, error) {
	switch settings.Font {
	case "bitmap":
		face, err := c1device.NewScaledBitmapFace(readerBitmap, settings.Size, settings.Size+(settings.Size+3)/4)
		return face, nil, err
	case "fusion12":
		face, err := c1device.NewPixel12Face(fusion12Bitmap)
		return face, nil, err
	}
	mapped := lab.fontData
	if mapped == nil || settings.Font != lab.settings.Font {
		var err error
		mapped, err = openLabMappedFont(filepath.Join(lab.fontsDir, settings.Font))
		if err != nil {
			return nil, nil, err
		}
	}
	face, err := mapped.typeface.NewFace(float64(settings.Size))
	if err != nil {
		if mapped != lab.fontData {
			mapped.Close()
		}
		return nil, nil, err
	}
	return face, mapped, nil
}

func (app *readerApp) applyLabSettings(settings labSettings, persist bool) error {
	if err := settings.validate(); err != nil {
		return err
	}
	lab := app.lab
	face, mapped := app.bodyFace, lab.fontData
	newFace := settings.Font != lab.settings.Font || settings.Size != lab.settings.Size
	if newFace {
		var err error
		face, mapped, err = lab.newFace(settings)
		if err != nil {
			return err
		}
	}
	installed := false
	defer func() {
		if newFace && !installed {
			face.Close()
			if mapped != lab.fontData {
				mapped.Close()
			}
		}
	}()
	if newFace && (face.LineHeight() < settings.Size || face.LineHeight() > readerBodyHeight) {
		return fmt.Errorf("字体行高不适合阅读区域")
	}
	height := readerBodyHeight
	if settings.Fullscreen {
		height = fullscreenBodyHeight
	}
	index := app.chapterPagination
	pages, end := app.pages, app.windowEnd
	bookmark, hasPosition := app.currentBookmark()
	layoutChanged := newFace || settings.Fullscreen != app.fullscreen
	if hasPosition && layoutChanged {
		var err error
		if !index.matches(app.document, app.document.Chapters[bookmark.Chapter], face, readerTextWidth, height) {
			index, err = countChapterPagesForLayout(app.document, app.document.Chapters[bookmark.Chapter], face, readerTextWidth, height)
		}
		if err == nil {
			pages, end, err = index.readWindow(bookmark.Offset)
		}
		if err != nil {
			return fmt.Errorf("分页失败，已保留设置与位置: %w", err)
		}
	}
	if persist {
		if err := writePrivateCache(filepath.Join(lab.home, "reader-settings.json"), func(w io.Writer) error {
			return json.NewEncoder(w).Encode(settings)
		}); err != nil {
			return fmt.Errorf("保存设置失败: %w", err)
		}
	}
	old, oldMapped := lab.ownedFace, lab.fontData
	lab.settings, lab.fontData = settings, mapped
	lab.typeface = nil
	if mapped != nil {
		lab.typeface = mapped.typeface
	}
	for i, font := range lab.fonts {
		if font == settings.Font {
			lab.fontPick = i
		}
	}
	app.bodyFace, app.fullscreen = face, settings.Fullscreen
	if layoutChanged {
		if hasPosition {
			app.chapterPagination = index
			app.pages, app.pageIndex = pages, 0
			app.windowStart, app.windowEnd = bookmark.Offset, end
			app.resumeOffset = bookmark.Offset
			app.dirty = true
		} else {
			app.chapterPagination = nil
		}
	}
	if newFace {
		lab.ownedFace = face
		if old != nil {
			old.Close()
		}
		if oldMapped != mapped {
			oldMapped.Close()
		}
	}
	installed = true
	app.message = ""
	return nil
}

func (app *readerApp) closeLab() {
	if app.lab == nil {
		return
	}
	lab := app.lab
	app.bodyFace = lab.originalFace
	app.chapterPagination = nil
	app.lab = nil
	lab.typeface = nil
	if lab.ownedFace != nil {
		lab.ownedFace.Close()
		lab.ownedFace = nil
	}
	lab.fontData.Close()
	lab.fontData = nil
}

func (app *readerApp) handleLabSettings(event c1device.Event) {
	lab := app.lab
	switch event.Key {
	case c1device.KeyBack:
		app.view = viewReader
	case c1device.KeyUp:
		lab.selected = moveSelection(lab.selected, -1, 4)
	case c1device.KeyDown:
		lab.selected = moveSelection(lab.selected, 1, 4)
	case c1device.KeyLeft, c1device.KeyRight:
		delta := 1
		if event.Key == c1device.KeyLeft {
			delta = -1
		}
		settings := lab.settings
		switch lab.selected {
		case 0:
			// Keep a failed candidate's cursor so one bad font cannot block later files.
			lab.fontPick = moveSelection(lab.fontPick, delta, len(lab.fonts))
			settings.Font = lab.fonts[lab.fontPick]
			if settings.Font == "fusion12" {
				settings.Size = 12
			}
		case 1:
			if settings.Font == "fusion12" {
				app.message = "Fusion原生点阵固定12px"
				return
			}
			for i, size := range labFontSizes {
				if size == settings.Size {
					settings.Size = labFontSizes[moveSelection(i, delta, len(labFontSizes))]
					break
				}
			}
		case 2:
			settings.Threshold += delta * 16
			if settings.Threshold < 32 {
				settings.Threshold = 32
			}
			if settings.Threshold > 224 {
				settings.Threshold = 224
			}
		case 3:
			settings.Fullscreen = !settings.Fullscreen
		}
		if settings != lab.settings {
			if err := app.applyLabSettings(settings, true); err != nil {
				app.message = err.Error()
			}
		}
	}
}

func (app *readerApp) renderLabSettings(canvas *c1device.Canvas) {
	settings := app.lab.settings
	font := settings.Font
	switch font {
	case "bitmap":
		font = "Unifont16 可缩放"
	case "fusion12":
		font = "Fusion 原生12"
	case "LXGWWenKaiLite-Regular.ttf":
		font = "霞鹜文楷 Regular"
	}
	threshold := fmt.Sprintf("黑白阈值  %d", settings.Threshold)
	if settings.Font == "bitmap" || settings.Font == "fusion12" {
		threshold += " (点阵无影响)"
	}
	full := "关闭"
	if settings.Fullscreen {
		full = "开启"
	}
	items := []string{"字体  " + font, fmt.Sprintf("字号  %d px", settings.Size), threshold, "全屏  " + full}
	app.renderList(canvas, "阅读设置", items, app.lab.selected, "项", "↑↓选择 ←→调整 BACK返回")
}
