package main

import (
	"fmt"
	"os"

	"c1device"
)

// Typeface borrows data; all faces and pagination caches must be detached before Close.
// Mapped font files must be replaced by rename, never truncated in place while in use.
type labMappedFont struct {
	typeface *c1device.Typeface
	data     []byte
}

func openLabMappedFont(path string) (*labMappedFont, error) {
	if err := c1device.RequireStoragePath(path); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxLabFontBytes {
		return nil, fmt.Errorf("字体须为不超过16MiB的普通文件")
	}
	file, err := openLabFontFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() != before.Size() {
		return nil, fmt.Errorf("字体文件在打开时发生变化")
	}
	data, err := loadLabFontData(file, int(info.Size()))
	if err != nil {
		return nil, err
	}
	mapped := &labMappedFont{data: data}
	typeface, err := c1device.ParseTypeface(data)
	if err != nil {
		mapped.Close()
		return nil, err
	}
	mapped.typeface = typeface
	return mapped, nil
}

func (mapped *labMappedFont) Close() error {
	if mapped == nil || mapped.data == nil {
		return nil
	}
	if err := releaseLabFontData(mapped.data); err != nil {
		return err
	}
	mapped.typeface = nil
	mapped.data = nil
	return nil
}
