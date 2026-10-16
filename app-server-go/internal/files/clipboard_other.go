//go:build !windows

package files

import (
	"context"
	"fmt"
)

type NativeClipboard struct{}

func (NativeClipboard) SetFiles(context.Context, []string) error {
	return fmt.Errorf("file clipboard is only supported on Windows")
}
