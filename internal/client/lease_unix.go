//go:build darwin || linux

package client

import (
	"errors"
	"os"
	"syscall"
)

var (
	ErrProfileInUse = errors.New("client: profile already in use")
	ErrUnsafeLease  = errors.New("client: unsafe profile lease")
)

type profileLease struct {
	file *os.File
}

func acquireProfileLease(path string) (*profileLease, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, ErrUnsafeLease
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		_ = file.Close()
		return nil, ErrUnsafeLease
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrProfileInUse
		}
		return nil, ErrUnsafeLease
	}
	return &profileLease{file: file}, nil
}

func (l *profileLease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return errors.Join(unlockErr, file.Close())
}
