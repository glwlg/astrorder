package files

import (
	"path/filepath"
	"strings"
)

// Preserve slash-based target paths; filepath.Dir on Windows rewrites Linux
// paths with backslashes before they reach the remote helper.
func fileParent(name string) string {
	if strings.HasPrefix(name, "/") {
		if index := strings.LastIndex(name, "/"); index > 0 {
			return name[:index]
		}
		return "/"
	}
	return filepath.Dir(name)
}
