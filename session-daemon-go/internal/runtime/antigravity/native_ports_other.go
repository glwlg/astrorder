//go:build !windows

package antigravity

import "fmt"

func nativePorts(pid int) ([]int, error) {
	return nil, fmt.Errorf("Antigravity native listener discovery is unavailable on this platform")
}
