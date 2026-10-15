//go:build windows

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

type StartupSettings struct {
	Available bool `json:"available"`
	Enabled   bool `json:"enabled"`
}

const runRegistryKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const appRegistryName = "Astrorder"

func getStartupSettings(isPackaged bool) (StartupSettings, error) {
	if !isPackaged {
		return StartupSettings{Available: false, Enabled: false}, nil
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, runRegistryKey, registry.QUERY_VALUE)
	if err != nil {
		return StartupSettings{Available: true, Enabled: false}, nil
	}
	defer k.Close()

	val, _, err := k.GetStringValue(appRegistryName)
	if err != nil {
		return StartupSettings{Available: true, Enabled: false}, nil
	}

	return StartupSettings{Available: true, Enabled: strings.TrimSpace(val) != ""}, nil
}

func setStartupEnabled(isPackaged bool, enabled bool) (StartupSettings, error) {
	if !isPackaged {
		return StartupSettings{Available: false, Enabled: false}, fmt.Errorf("startup settings only supported in packaged installations")
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, runRegistryKey, registry.SET_VALUE)
	if err != nil {
		return StartupSettings{Available: true, Enabled: false}, fmt.Errorf("open registry key: %w", err)
	}
	defer k.Close()

	if enabled {
		exe, err := os.Executable()
		if err != nil {
			return StartupSettings{Available: true, Enabled: false}, fmt.Errorf("get executable path: %w", err)
		}
		cmdVal := fmt.Sprintf("\"%s\" --packaged", exe)
		if err := k.SetStringValue(appRegistryName, cmdVal); err != nil {
			return StartupSettings{Available: true, Enabled: false}, fmt.Errorf("set registry value: %w", err)
		}
		return StartupSettings{Available: true, Enabled: true}, nil
	}

	_ = k.DeleteValue(appRegistryName)
	return StartupSettings{Available: true, Enabled: false}, nil
}

func setClipboardFilesWindows(paths []string) (bool, error) {
	var valid []string
	for _, p := range paths {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if strings.ContainsAny(trimmed, "\r\n\x00") || !filepath.IsAbs(trimmed) {
			return false, fmt.Errorf("invalid clipboard file path")
		}
		valid = append(valid, trimmed)
	}
	if len(valid) == 0 {
		return false, nil
	}

	// Newline-separated: Windows paths may contain ';' but never a newline.
	psScript := `
$paths = $env:ASTRORDER_CLIP_FILES -split "` + "`" + `n" | Where-Object { $_.Trim() -ne "" }
Add-Type -AssemblyName System.Windows.Forms
$strCol = New-Object System.Collections.Specialized.StringCollection
foreach ($p in $paths) { $strCol.Add($p.Trim()) }
if ($strCol.Count -gt 0) {
    $data = New-Object System.Windows.Forms.DataObject
    $data.SetFileDropList($strCol)
    [System.Windows.Forms.Clipboard]::SetDataObject($data, $true)
    Write-Output "OK"
}
`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	cmd.Env = append(os.Environ(), "ASTRORDER_CLIP_FILES="+strings.Join(valid, "\n"))

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return false, err
	}
	return strings.Contains(stdout.String(), "OK"), nil
}
