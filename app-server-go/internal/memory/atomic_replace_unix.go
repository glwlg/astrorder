//go:build !windows

package memory

import "os"

func atomicReplace(source, destination string) error {
	return os.Rename(source, destination)
}
