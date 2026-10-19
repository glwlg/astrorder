//go:build windows

package antigravity

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"
)

// Resolve listeners from the OS owner PID, never from a shared log file.
func nativePorts(pid int) ([]int, error) {
	proc := syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")
	ports := []int{}
	for _, family := range []uint32{2, 23} {
		var size uint32
		code, _, _ := proc.Call(0, uintptr(unsafe.Pointer(&size)), 1, uintptr(family), 3, 0)
		if code != 0 && code != 122 {
			return nil, syscall.Errno(code)
		}
		if size == 0 {
			continue
		}
		buf := make([]byte, size)
		code, _, _ = proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 1, uintptr(family), 3, 0)
		if code != 0 {
			return nil, syscall.Errno(code)
		}
		row, port, owner := 24, 8, 20
		if family == 23 {
			row, port, owner = 56, 20, 52
		}
		if len(buf) < 4 {
			return nil, fmt.Errorf("invalid native listener table")
		}
		for i := 0; i < int(binary.LittleEndian.Uint32(buf)); i++ {
			offset := 4 + i*row
			if offset+row > len(buf) {
				return nil, fmt.Errorf("truncated native listener table")
			}
			if int(binary.LittleEndian.Uint32(buf[offset+owner:])) == pid {
				ports = append(ports, int(binary.BigEndian.Uint16(buf[offset+port:])))
			}
		}
	}
	return ports, nil
}
