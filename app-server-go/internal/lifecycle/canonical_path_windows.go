//go:build windows

package lifecycle

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	fileShareRead           = 0x00000001
	fileShareWrite          = 0x00000002
	fileShareDelete         = 0x00000004
	openExisting            = 3
	fileFlagBackupSemantics = 0x02000000
)

var getFinalPathNameByHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

func canonicalExistingPath(path string) (string, error) {
	pathPointer, err := syscall.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	handle, err := syscall.CreateFile(
		pathPointer,
		0,
		fileShareRead|fileShareWrite|fileShareDelete,
		nil,
		openExisting,
		fileFlagBackupSemantics,
		0,
	)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(handle)

	buffer := make([]uint16, 512)
	for {
		length, _, callErr := getFinalPathNameByHandle.Call(
			uintptr(handle),
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(len(buffer)),
			0,
		)
		if length == 0 {
			return "", fmt.Errorf("resolve native final path: %w", callErr)
		}
		if int(length) < len(buffer) {
			resolved := syscall.UTF16ToString(buffer[:length])
			switch {
			case strings.HasPrefix(resolved, `\\?\UNC\`):
				resolved = `\\` + resolved[len(`\\?\UNC\`):]
			case strings.HasPrefix(resolved, `\\?\`):
				resolved = resolved[len(`\\?\`):]
			}
			return filepath.Clean(resolved), nil
		}
		buffer = make([]uint16, int(length)+1)
	}
}
