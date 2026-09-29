//go:build linux

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func labTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func labTestHold(t *testing.T, dir, name string, how int) *os.File {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return f
}

func labTestMode(t *testing.T, dir, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, labModeName))
	if want == "" {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("mode should be absent: %q, %v", data, err)
		}
	} else if err != nil || string(data) != want {
		t.Fatalf("mode = %q, %v; want %q", data, err, want)
	}
}

func TestLabLeaseAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	labTestFile(t, dir, labModeName, "terminal")
	closeLease, err := acquireLabLeaseAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeLease)
	labTestMode(t, dir, "direct")
	for _, name := range []string{labRunName, labHardwareName} {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		f.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			t.Fatalf("%s not exclusively held: %v", name, err)
		}
	}
	guard := labTestHold(t, dir, labGuardName, syscall.LOCK_EX)
	guard.Close()
	closeLease()
	labTestMode(t, dir, "")
	for _, name := range []string{labGuardName, labRunName, labHardwareName} {
		f := labTestHold(t, dir, name, syscall.LOCK_EX)
		st, err := f.Stat()
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatalf("lock file not retained safely: %s %v", name, err)
		}
		f.Close()
	}
	next, err := acquireLabLeaseAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next()
	closeLease()
	labTestMode(t, dir, "direct")
}

func TestLabLeaseBusy(t *testing.T) {
	for _, name := range []string{labGuardName, labRunName, labHardwareName} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			labTestFile(t, dir, labModeName, "terminal")
			holder := labTestHold(t, dir, name, syscall.LOCK_EX)
			start := time.Now()
			closeLease, err := acquireLabLeaseAt(dir)
			if err == nil || closeLease != nil {
				t.Fatal("busy lease must reject startup")
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("lease wait was not bounded")
			}
			if name == labHardwareName {
				labTestMode(t, dir, "")
			} else {
				labTestMode(t, dir, "terminal")
			}
			holder.Close()
			next, err := acquireLabLeaseAt(dir)
			if err != nil {
				t.Fatalf("failed acquisition leaked locks: %v", err)
			}
			next()
		})
	}
}

func TestLabLeaseUnsafeFiles(t *testing.T) {
	for _, name := range []string{labGuardName, labRunName, labHardwareName, labModeName} {
		for _, kind := range []string{"symlink", "hardlink", "permissions", "directory", "fifo", "owner"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, name)
				target := labTestFile(t, dir, "untouched", "sentinel")
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(target, path)
				case "hardlink":
					err = os.Link(target, path)
				case "permissions":
					labTestFile(t, dir, name, "sentinel")
					err = os.Chmod(path, 0644)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "fifo":
					err = syscall.Mkfifo(path, 0600)
				case "owner":
					if os.Geteuid() != 0 {
						t.Skip("owner mismatch requires root chown")
					}
					labTestFile(t, dir, name, "sentinel")
					err = os.Chown(path, 1, -1)
				}
				if err != nil {
					t.Fatal(err)
				}
				closeLease, err := acquireLabLeaseAt(dir)
				if err == nil || closeLease != nil {
					t.Fatal("unsafe file accepted")
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "sentinel" {
					t.Fatalf("target was modified: %q %v", data, err)
				}
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("unsafe entry removed: %v", err)
				}
				if kind == "permissions" || kind == "owner" {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != "sentinel" {
						t.Fatalf("unsafe entry truncated: %q %v", data, err)
					}
				}
			})
		}
	}
}

func TestLabLeaseDirectoryErrors(t *testing.T) {
	dir := t.TempDir()
	file := labTestFile(t, dir, "not-directory", "sentinel")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "absent"), file, link} {
		if release, err := acquireLabLeaseAt(path); err == nil || release != nil {
			t.Fatalf("unsafe directory accepted: %s", path)
		}
	}
}

func TestLabLeaseTransientHardwareGuard(t *testing.T) {
	dir := t.TempDir()
	holder := labTestHold(t, dir, labHardwareName, syscall.LOCK_SH)
	type result struct {
		release func()
		err     error
	}
	done := make(chan result, 1)
	go func() {
		release, err := acquireLabLeaseAt(dir)
		done <- result{release, err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	seen := false
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(filepath.Join(dir, labModeName))
		if string(data) == "direct" {
			seen = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	holder.Close()
	r := <-done
	if r.release != nil {
		defer r.release()
	}
	if !seen || r.err != nil {
		t.Fatalf("transient shared lease not drained after direct: seen=%v err=%v", seen, r.err)
	}
}

func TestLabLeaseConcurrentAcquisition(t *testing.T) {
	dir := t.TempDir()
	start := make(chan struct{})
	results := make(chan func(), 8)
	for i := 0; i < cap(results); i++ {
		go func() {
			<-start
			release, _ := acquireLabLeaseAt(dir)
			results <- release
		}()
	}
	close(start)
	var winner func()
	count := 0
	for i := 0; i < cap(results); i++ {
		if release := <-results; release != nil {
			count++
			winner = release
			t.Cleanup(release)
		}
	}
	if count != 1 {
		t.Fatalf("got %d owners, want 1", count)
	}
	labTestMode(t, dir, "direct")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); winner() }()
	}
	wg.Wait()
	labTestMode(t, dir, "")
}

func TestLabLeaseReleaseGuardBusy(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLabLeaseAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	guard := labTestHold(t, dir, labGuardName, syscall.LOCK_EX)
	release()
	labTestMode(t, dir, "direct")
	guard.Close()
	next, err := acquireLabLeaseAt(dir)
	if err != nil {
		t.Fatalf("release leaked locks: %v", err)
	}
	next()
	labTestMode(t, dir, "")
}

func TestLabLeaseExecInheritance(t *testing.T) {
	for _, scenario := range []string{"success", "missing-both", "missing-run", "missing-hardware", "wrong-inode", "unlocked-description", "permissions", "hardlink", "symlink", "duplicate", "fresh"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			labTestFile(t, dir, labModeName, "terminal")
			run := labTestHold(t, dir, labRunName, syscall.LOCK_EX)
			hardware := labTestHold(t, dir, labHardwareName, syscall.LOCK_EX)
			extra := []*os.File{run, hardware}
			runPath := filepath.Join(dir, labRunName)
			switch scenario {
			case "missing-both":
				extra = nil
			case "missing-run":
				extra = []*os.File{hardware}
			case "missing-hardware":
				extra = []*os.File{run}
			case "wrong-inode":
				extra = []*os.File{labTestHold(t, dir, "unrelated", syscall.LOCK_EX), hardware}
			case "unlocked-description":
				other, err := os.OpenFile(runPath, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { other.Close() })
				extra = []*os.File{other, hardware}
			case "permissions":
				if err := run.Chmod(0644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(runPath, filepath.Join(dir, "alias")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(dir, "real-run")
				if err := os.Rename(runPath, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, runPath); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				extra = []*os.File{run, hardware, run}
			case "fresh":
				run.Close()
				hardware.Close()
				extra = nil
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "-test.run=^TestLabLeaseExecChild$", "-test.timeout=10s")
			cmd.Env = append(os.Environ(), "LAB_LEASE_EXEC_CASE="+scenario, "LAB_LEASE_EXEC_DIR="+dir)
			cmd.ExtraFiles = extra
			gate, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				gate.Close()
				t.Fatal(err)
			}
			if scenario == "success" {
				run.Close()
				hardware.Close()
			}
			gate.Close()
			if err := cmd.Wait(); err != nil {
				t.Fatalf("exec child: %v\n%s", err, output.String())
			}
			if scenario == "success" || scenario == "fresh" || scenario == "missing-hardware" {
				labTestMode(t, dir, "")
			} else {
				labTestMode(t, dir, "terminal")
			}
		})
	}
}

func TestLabLeaseExecChild(t *testing.T) {
	scenario := os.Getenv("LAB_LEASE_EXEC_CASE")
	if scenario == "" {
		t.Skip("exec helper")
	}
	dir := os.Getenv("LAB_LEASE_EXEC_DIR")
	if dir == "" {
		t.Fatal("missing isolated test directory")
	}
	if _, err := io.ReadAll(os.Stdin); err != nil {
		t.Fatal(err)
	}
	release, err := acquireLabLeaseWithInheritanceAt(dir, true)
	if scenario != "success" && scenario != "fresh" {
		if err == nil || release != nil {
			if release != nil {
				release()
			}
			t.Fatal("unsafe or unavailable inherited lease accepted")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	labTestMode(t, dir, "direct")
	if competing, err := acquireLabLeaseAt(dir); err == nil || competing != nil {
		if competing != nil {
			competing()
		}
		t.Fatal("ordinary directory acquisition reused an inherited lease")
	}
	if scenario == "success" {
		for _, fd := range []int{3, 4} {
			flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
			if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
				t.Fatalf("fd %d not CLOEXEC: %v", fd, errno)
			}
		}
	}
	for _, name := range []string{labRunName, labHardwareName} {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		f.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			t.Fatalf("%s not held: %v", name, err)
		}
	}
	release()
	release()
	if scenario == "success" {
		for _, fd := range []int{3, 4} {
			_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
			if errno != syscall.EBADF {
				t.Fatalf("inherited fd %d leaked: %v", fd, errno)
			}
		}
	}
	labTestMode(t, dir, "")
	next, err := acquireLabLeaseAt(dir)
	if err != nil {
		t.Fatalf("inherited locks not released: %v", err)
	}
	next()
}

func TestLabLeaseReleasePreservesReplacement(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLabLeaseAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	path := filepath.Join(dir, labModeName)
	if err := os.Rename(path, filepath.Join(dir, "original-mode")); err != nil {
		t.Fatal(err)
	}
	labTestFile(t, dir, labModeName, "terminal")
	release()
	labTestMode(t, dir, "terminal")
}
