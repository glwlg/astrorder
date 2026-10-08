package modelconfig

import (
	"errors"
	"os"
)

// CreateTemp uses exclusive creation; never follow a predictable staging name.
func stagePrivateFile(dir, pattern string, data []byte) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
