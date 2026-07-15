//go:build !linux && !darwin

package update

import "errors"

type updateLock struct{}

func acquireUpdateLock(string) (*updateLock, error) {
	return nil, errors.New("interprocess update locking is unsupported on this platform")
}

func (lock *updateLock) Close() error {
	return nil
}
