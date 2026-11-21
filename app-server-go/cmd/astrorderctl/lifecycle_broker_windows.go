//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"astrorder.dev/app-server/internal/lifecycle"
	"golang.org/x/sys/windows"
)

var isProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

func lifecycleControllerInJob() (bool, error) {
	var inJob int32
	ok, _, err := isProcessInJob.Call(uintptr(windows.CurrentProcess()), 0, uintptr(unsafe.Pointer(&inJob)))
	if ok == 0 {
		return false, err
	}
	return inJob != 0, nil
}

func invokeLifecycleBroker(parent context.Context, target, operation, root, state string, confirm bool) (int, []byte, error) {
	timeout := 70 * time.Second
	if target == "app" && operation == "restart" {
		timeout = 115 * time.Second
	} else if target == "daemon" && (operation == "restart" || operation == "update") {
		timeout = 150 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return 1, nil, fmt.Errorf("open independent controller reply: %w", err)
	}
	defer listener.Close()

	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return 1, nil, fmt.Errorf("create independent controller reply token: %w", err)
	}
	token := hex.EncodeToString(random[:])
	executable, err := os.Executable()
	if err != nil {
		return 1, nil, fmt.Errorf("resolve lifecycle controller: %w", err)
	}
	args := []string{
		executable, "lifecycle-broker",
		"--target", target, "--action", operation,
		"--root", root, "--state-dir", state,
		"--reply", listener.Addr().String(), "--token", token,
	}
	if confirm {
		args = append(args, "--confirm-active")
	}
	for index := range args {
		args[index] = syscall.EscapeArg(args[index])
	}
	commandLine := strings.Join(args, " ")
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		return 1, nil, fmt.Errorf("SystemRoot is required to launch the independent controller")
	}
	powershell := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	const script = `
$ErrorActionPreference = 'Stop'
$startup = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0 }
$result = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
    CommandLine = $env:ASTRORDER_BROKER_COMMAND
    CurrentDirectory = $env:ASTRORDER_BROKER_DIRECTORY
    ProcessStartupInformation = $startup
}
if ($result.ReturnValue -ne 0) { throw ("Win32_Process.Create returned " + $result.ReturnValue) }
[Console]::Out.WriteLine($result.ProcessId)
`
	launchCtx, launchCancel := context.WithTimeout(ctx, 20*time.Second)
	defer launchCancel()
	command := exec.CommandContext(launchCtx, powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	command.Env = append(lifecycle.ProcessEnvironment(nil),
		"ASTRORDER_BROKER_COMMAND="+commandLine,
		"ASTRORDER_BROKER_DIRECTORY="+root,
	)
	out, err := command.CombinedOutput()
	if err != nil {
		detail := strings.ReplaceAll(strings.TrimSpace(string(out)), token, "[redacted]")
		if len(detail) > 1024 {
			detail = detail[:1024]
		}
		return 1, nil, fmt.Errorf("launch independent lifecycle controller through Windows: %w: %s", err, detail)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		return 1, nil, fmt.Errorf("Windows did not return an independent controller process ID")
	}
	return waitLifecycleBrokerReply(ctx, listener, token)
}
