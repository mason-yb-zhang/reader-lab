//go:build linux

package main

import (
	"os"
	"syscall"
)

func openLabFontFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func loadLabFontData(file *os.File, size int) ([]byte, error) {
	return syscall.Mmap(int(file.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_PRIVATE)
}

func releaseLabFontData(data []byte) error {
	return syscall.Munmap(data)
}
