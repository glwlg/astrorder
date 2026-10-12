package memory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// LocalTargetFS performs native atomic writes and is safe only for explicitly local targets.
type LocalTargetFS struct{}

func (LocalTargetFS) ReadFile(_ context.Context, target Target, filename string) ([]byte, error) {
	if target.Kind != TargetLocal {
		return nil, fmt.Errorf("local filesystem cannot read %s target", target.Kind)
	}
	raw, err := os.ReadFile(filepath.FromSlash(filename))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrTargetFileNotFound
	}
	return raw, err
}

func (LocalTargetFS) WriteFileAtomic(_ context.Context, target Target, filename string, raw []byte, mode uint32) error {
	if target.Kind != TargetLocal {
		return fmt.Errorf("local filesystem cannot write %s target", target.Kind)
	}
	filename = filepath.FromSlash(filename)
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".astrorder-memory-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(os.FileMode(mode)); err != nil {
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := atomicReplace(temporaryName, filename); err != nil {
		return fmt.Errorf("atomic replace target file: %w", err)
	}
	if err := os.Chmod(filename, os.FileMode(mode)); err != nil {
		return fmt.Errorf("apply target file mode: %w", err)
	}
	committed = true
	return nil
}
