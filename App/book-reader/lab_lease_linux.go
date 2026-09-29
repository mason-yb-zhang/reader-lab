//go:build linux

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	labRunName      = "c1ancher-external-app.runlock"
	labGuardName    = "c1ancher-external-app.mode-guard"
	labHardwareName = "c1ancher-external-app.lock"
	labModeName     = "c1ancher-external-app.mode"
	labGuardWait    = 100 * time.Millisecond
	labHardwareWait = 250 * time.Millisecond
)

type labLease struct {
	dir, run, hardware int
	mode               syscall.Stat_t
	published          bool
	once               sync.Once
}

func acquireLabLease() (func(), error) {
	return acquireLabLeaseWithInheritanceAt("/dev/shm", true)
}

func acquireLabLeaseAt(dir string) (func(), error) {
	return acquireLabLeaseWithInheritanceAt(dir, false)
}

func acquireLabLeaseWithInheritanceAt(dir string, allowInherited bool) (func(), error) {
	var inherited []int
	if allowInherited {
		var err error
		inherited, err = labInheritedFDs()
		if err != nil {
			return nil, err
		}
	}
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open lease directory: %w", err)
	}
	lease := &labLease{dir: fd, run: -1, hardware: -1}
	guard, err := lease.lock(labGuardName, labGuardWait)
	if err != nil {
		syscall.Close(fd)
		return nil, err
	}
	lease.run, err = lease.lockOrInherit(labRunName, 0, inherited)
	if err == nil {
		err = lease.publishMode()
	}
	syscall.Close(guard)
	if err == nil {
		// Publishing direct first lets the desktop finish its short shared lease.
		lease.hardware, err = lease.lockOrInherit(labHardwareName, labHardwareWait, inherited)
	}
	if err != nil {
		return nil, errors.Join(err, lease.release())
	}
	return func() {
		if err := lease.release(); err != nil {
			log.Printf("reader-lab lease cleanup: %v", err)
		}
	}, nil
}

// Only descriptors surviving exec are candidates; ordinary Go opens have CLOEXEC.
func labInheritedFDs() ([]int, error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return nil, fmt.Errorf("inspect inherited leases: %w", err)
	}
	var fds []int
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd < 3 {
			continue
		}
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		if errno == syscall.EBADF {
			continue
		}
		if errno != 0 {
			return nil, fmt.Errorf("inspect inherited fd %d: %w", fd, errno)
		}
		if flags&syscall.FD_CLOEXEC == 0 {
			fds = append(fds, fd)
		}
	}
	return fds, nil
}

func (l *labLease) lockOrInherit(name string, wait time.Duration, inherited []int) (int, error) {
	if len(inherited) == 0 {
		return l.lock(name, wait)
	}
	pathFD, expected, err := l.open(name, false)
	if errors.Is(err, syscall.ENOENT) {
		return l.lock(name, wait)
	}
	if err != nil {
		return -1, err
	}
	defer syscall.Close(pathFD)
	match := -1
	for _, fd := range inherited {
		var st syscall.Stat_t
		if err := syscall.Fstat(fd, &st); err != nil {
			return -1, fmt.Errorf("stat inherited fd %d: %w", fd, err)
		}
		if st.Dev != expected.Dev || st.Ino != expected.Ino {
			continue
		}
		if st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Nlink != 1 || st.Uid != uint32(syscall.Geteuid()) || st.Mode&0777 != 0600 {
			return -1, fmt.Errorf("unsafe inherited %s: %w", name, syscall.EACCES)
		}
		if match >= 0 {
			return -1, fmt.Errorf("multiple inherited descriptors for %s", name)
		}
		match = fd
	}
	if match < 0 {
		return l.lock(name, wait)
	}
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(match), syscall.F_SETFD, syscall.FD_CLOEXEC)
	if errno != 0 {
		syscall.Close(match)
		return -1, fmt.Errorf("protect inherited %s: %w", name, errno)
	}
	if err := syscall.Flock(match, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		syscall.Close(match)
		return -1, fmt.Errorf("lock inherited %s: %w", name, err)
	}
	return match, nil
}

func (l *labLease) open(name string, create bool) (int, syscall.Stat_t, error) {
	flags := syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	if create {
		flags |= syscall.O_CREAT
	}
	fd, err := syscall.Openat(l.dir, name, flags, 0600)
	var st syscall.Stat_t
	if err != nil {
		return -1, st, fmt.Errorf("open %s: %w", name, err)
	}
	err = syscall.Fstat(fd, &st)
	if err == nil && (st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Nlink != 1 || st.Uid != uint32(syscall.Geteuid()) || st.Mode&0777 != 0600) {
		err = syscall.EACCES
	}
	if err != nil {
		syscall.Close(fd)
		return -1, st, fmt.Errorf("validate %s: %w", name, err)
	}
	return fd, st, nil
}

func (l *labLease) lock(name string, wait time.Duration) (int, error) {
	fd, _, err := l.open(name, true)
	if err != nil {
		return -1, err
	}
	deadline := time.Now().Add(wait)
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return fd, nil
		}
		if (!errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR)) || !time.Now().Before(deadline) {
			syscall.Close(fd)
			return -1, fmt.Errorf("lock %s: %w", name, err)
		}
		time.Sleep(time.Millisecond)
	}
}

// The caller holds both mode-guard and its own runlock throughout publication.
func (l *labLease) publishMode() error {
	if l.run < 0 {
		return errors.New("cannot publish mode without runlock")
	}
	fd, _, err := l.open(labModeName, false)
	if err == nil {
		syscall.Close(fd)
		err = syscall.Unlinkat(l.dir, labModeName)
	}
	if err != nil && !errors.Is(err, syscall.ENOENT) {
		return fmt.Errorf("clear stale mode: %w", err)
	}
	fd, st, err := l.open(labModeName, true)
	if err != nil {
		return err
	}
	l.mode, l.published = st, true
	err = syscall.Ftruncate(fd, 0)
	if err == nil {
		var n int
		n, err = syscall.Write(fd, []byte("direct"))
		if err == nil && n != len("direct") {
			err = errors.New("short mode write")
		}
	}
	return errors.Join(err, syscall.Close(fd))
}

func (l *labLease) clearMode() error {
	guard, err := l.lock(labGuardName, labGuardWait)
	if err != nil {
		return err
	}
	defer syscall.Close(guard)
	fd, st, err := l.open(labModeName, false)
	if errors.Is(err, syscall.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	syscall.Close(fd)
	if st.Dev != l.mode.Dev || st.Ino != l.mode.Ino {
		return errors.New("mode file replaced; refusing to remove it")
	}
	return syscall.Unlinkat(l.dir, labModeName)
}

func (l *labLease) release() (err error) {
	l.once.Do(func() {
		// Clear only our publication, before letting another runlock owner in.
		if l.run >= 0 && l.published {
			err = l.clearMode()
		}
		if l.hardware >= 0 {
			err = errors.Join(err, syscall.Close(l.hardware))
			l.hardware = -1
		}
		if l.run >= 0 {
			err = errors.Join(err, syscall.Close(l.run))
			l.run = -1
		}
		err = errors.Join(err, syscall.Close(l.dir))
		l.dir = -1
	})
	return err
}
