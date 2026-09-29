//go:build linux

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

func labMappingCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, path) {
			count++
			if strings.Fields(line)[1] != "r--p" {
				t.Fatal("font mapping is not private read-only:", line)
			}
		}
	}
	return count
}

func TestLabLinuxLargeFontUsesMappingNotHeapCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.ttf")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(goregular.TTF); err == nil {
		err = file.Truncate(14 << 20)
	}
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	mapped, err := openLabMappedFont(path)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	defer mapped.Close()
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
		t.Fatalf("mapping a 14MiB font allocated %d heap bytes", allocated)
	}
	if labMappingCount(t, path) != 1 {
		t.Fatal("font is not backed by one file mapping")
	}
	if err := mapped.Close(); err != nil {
		t.Fatal(err)
	}
	if labMappingCount(t, path) != 0 {
		t.Fatal("closed font mapping leaked")
	}
}

func TestLabLinuxFailedFontTransitionsUnmapCandidate(t *testing.T) {
	for _, failure := range []string{"parse", "paginate", "save"} {
		t.Run(failure, func(t *testing.T) {
			app := labFixture(t)
			settings := app.lab.settings
			settings.Font = "Go.ttf"
			if err := app.applyLabSettings(settings, true); err != nil {
				t.Fatal(err)
			}
			oldPath := filepath.Join(app.lab.fontsDir, "Go.ttf")
			path := filepath.Join(app.lab.fontsDir, "Next.ttf")
			data := goregular.TTF
			if failure == "parse" {
				data = []byte("not a font")
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if failure == "paginate" {
				if err := os.Remove(app.document.Path); err != nil {
					t.Fatal(err)
				}
			} else if failure == "save" {
				app.lab.home = app.document.Path
			}
			settings.Font = "Next.ttf"
			if err := app.applyLabSettings(settings, true); err == nil {
				t.Fatal("expected failure")
			}
			if labMappingCount(t, path) != 0 || labMappingCount(t, oldPath) != 1 {
				t.Fatal("failed transition leaked new mapping or released old mapping")
			}
			app.closeLab()
			if labMappingCount(t, oldPath) != 0 {
				t.Fatal("closeLab leaked the active mapping")
			}
		})
	}
}
