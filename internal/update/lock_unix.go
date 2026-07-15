//go:build linux || darwin

package update

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

type updateLock struct {
	file *os.File
}

func acquireUpdateLock(execPath string) (*updateLock, error) {
	lockPath := execPath + ".update.lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open update lock: %w", err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errors.New("another update is already in progress")
		}
		return nil, fmt.Errorf("acquire update lock: %w", err)
	}

	return &updateLock{file: file}, nil
}

func (lock *updateLock) Close() error {
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("release update lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close update lock: %w", closeErr)
	}
	return nil
}
