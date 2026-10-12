//go:build !windows

package lifecycle

import "path/filepath"

func canonicalExistingPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
