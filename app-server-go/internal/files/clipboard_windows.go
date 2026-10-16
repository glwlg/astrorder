//go:build windows

package files

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type NativeClipboard struct{}

func (NativeClipboard) SetFiles(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no clipboard files")
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) || strings.ContainsAny(p, "\x00\r\n") {
			return fmt.Errorf("invalid clipboard file path")
		}
		if _, err := os.Stat(p); err != nil {
			return err
		}
	}
	const script = `$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.Windows.Forms; $files=New-Object System.Collections.Specialized.StringCollection; foreach($p in ($env:ASTRORDER_CLIP_FILES -split "` + "`" + `n")) { [void]$files.Add($p) }; $data=New-Object System.Windows.Forms.DataObject; $data.SetFileDropList($files); [System.Windows.Forms.Clipboard]::SetDataObject($data,$true)`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	command.Env = append(os.Environ(), "ASTRORDER_CLIP_FILES="+strings.Join(paths, "\n"))
	configureProcess(command)
	return command.Run()
}
