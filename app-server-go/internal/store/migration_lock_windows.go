//go:build windows

package store

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

type schemaMigrationLock struct {
	file *os.File
}

func acquireSchemaMigrationLock(databasePath string) (*schemaMigrationLock, error) {
	file, err := os.OpenFile(databasePath+".migrate.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open schema migration lock: %w", err)
	}
	overlapped := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errors.New("schema migration is owned by another process")
		}
		return nil, fmt.Errorf("acquire schema migration lock: %w", err)
	}
	return &schemaMigrationLock{file: file}, nil
}

func (lock *schemaMigrationLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	overlapped := new(windows.Overlapped)
	unlockErr := windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, overlapped)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return fmt.Errorf("release schema migration lock: %w", unlockErr)
	}
	return closeErr
}
