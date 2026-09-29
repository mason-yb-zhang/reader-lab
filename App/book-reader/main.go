package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"c1device"
)

var version = "dev"

const (
	defaultBooksDir       = "/storage/mtp/Book"
	defaultBookReaderHome = "/storage/c1/reader-lab/state"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("reader-lab %s (based on book-reader 0.1.23)\n", version)
		return
	}
	// Leave room for the desktop and kernel on the roughly 50 MiB device.
	debug.SetMemoryLimit(32 << 20)
	if err := runWithDiagnostics(); err != nil {
		fmt.Fprintf(os.Stderr, "book-reader: %v\n", err)
		os.Exit(1)
	}
}

func run() (err error) {
	booksDir, home, err := prepareReaderStorage()
	if err != nil {
		return err
	}
	releaseLease, err := acquireLabLease()
	if err != nil {
		return fmt.Errorf("reader-lab cannot take display: %w", err)
	}
	defer releaseLease()
	uiFace, err := newReaderFace(false)
	if err != nil {
		return err
	}
	defer uiFace.Close()
	bodyFace, err := newReaderFace(true)
	if err != nil {
		return err
	}
	defer bodyFace.Close()
	app, err := newReaderApp(
		booksDir,
		uiFace,
		bodyFace,
		ProgressStore{Dir: home},
		BookmarkStore{Dir: home},
	)
	if err != nil {
		return fmt.Errorf("scan library: %w", err)
	}
	fontsDir, err := labFontsDir()
	if err != nil {
		return err
	}
	if err := app.initLab(home, fontsDir); err != nil {
		return fmt.Errorf("prepare lab settings: %w", err)
	}
	defer app.closeLab()
	platform, err := c1device.OpenPlatform()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := platform.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("restore display: %w", closeErr))
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := drawLabFrame(platform, app, true); err != nil {
		return err
	}
	var saveTimer *time.Timer
	var saveChannel <-chan time.Time
	refreshes := 0
	save := func() {
		if err := app.saveProgress(); err != nil {
			app.message = err.Error()
		}
	}
	defer func() {
		if saveTimer != nil {
			saveTimer.Stop()
		}
		err = saveProgressOnExit(app, err)
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-saveChannel:
			save()
			saveChannel = nil
			if app.message != "" {
				if drawErr := drawLabFrame(platform, app, false); drawErr != nil {
					return drawErr
				}
			}
		case event, ok := <-platform.Events():
			if !ok {
				return fmt.Errorf("input event stream closed unexpectedly")
			}
			if app.handleEvent(event) {
				return nil
			}
			if app.dirty {
				if saveTimer == nil {
					saveTimer = time.NewTimer(750 * time.Millisecond)
				} else {
					if !saveTimer.Stop() {
						select {
						case <-saveTimer.C:
						default:
						}
					}
					saveTimer.Reset(750 * time.Millisecond)
				}
				saveChannel = saveTimer.C
			}
			refreshes++
			if err := drawLabFrame(platform, app, refreshes%12 == 0); err != nil {
				return fmt.Errorf("draw view=%d chapter=%d page=%d key=%d rune=%q: %w", app.view, app.chapterIndex, app.pageIndex, event.Key, event.Rune, err)
			}
		}
	}
}

func saveProgressOnExit(app *readerApp, prior error) error {
	if err := app.saveProgress(); err != nil {
		return errors.Join(prior, fmt.Errorf("save progress on exit: %w", err))
	}
	return prior
}

func labFontsDir() (string, error) {
	if path := os.Getenv("C1_LAB_FONTS_DIR"); path != "" {
		return path, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "assets", "fonts")), nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
