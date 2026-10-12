//go:build !windows

package store

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

type schemaMigrationLock struct {
	file *os.File
}

func acquireSchemaMigrationLock(databasePath string) (*schemaMigrationLock, error) {
	file, err := os.OpenFile(databasePath+".migrate.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open schema migration lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
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
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return fmt.Errorf("release schema migration lock: %w", unlockErr)
	}
	return closeErr
}
