//go:build windows

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsTerminal struct {
	hPC      windows.Handle
	inWrite  *os.File
	outRead  *os.File
	process  windows.Handle
	thread   windows.Handle
	closeMu  sync.Mutex
	closed   bool
	exited   bool
	disposed bool
	done     chan struct{}
}

func defaultPlatformTerminal(shell string, cwd string, env []string, cols, rows int) (Terminal, error) {
	resolved, err := exec.LookPath(shell)
	if err != nil {
		return nil, err
	}
	shell = resolved
	application, err := windows.UTF16PtrFromString(shell)
	if err != nil {
		return nil, err
	}
	environment, err := windowsEnvironment(env)
	if err != nil {
		return nil, err
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	var inRead, inWrite windows.Handle
	var outRead, outWrite windows.Handle

	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("create in pipe: %w", err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		return nil, fmt.Errorf("create out pipe: %w", err)
	}

	var hPC windows.Handle
	coord := windows.Coord{X: int16(cols), Y: int16(rows)}
	if err := windows.CreatePseudoConsole(coord, inRead, outWrite, 0, &hPC); err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		windows.CloseHandle(outWrite)
		return nil, fmt.Errorf("create pseudo console: %w", err)
	}

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hPC)
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		windows.CloseHandle(outWrite)
		return nil, fmt.Errorf("new proc thread attribute list: %w", err)
	}
	defer attrList.Delete()

	// PSEUDOCONSOLE is a handle-valued attribute, not a pointer to Go data.
	// Call the native ABI with uintptr to avoid manufacturing an invalid Go pointer.
	update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
	updated, _, updateErr := update.Call(uintptr(unsafe.Pointer(attrList.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(hPC), unsafe.Sizeof(hPC), 0, 0)
	if updated == 0 {
		err := updateErr
		windows.ClosePseudoConsole(hPC)
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		windows.CloseHandle(outWrite)
		return nil, fmt.Errorf("update proc thread attribute: %w", err)
	}

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = attrList.List()

	cmdLine, err := windows.UTF16PtrFromString(syscall.EscapeArg(shell))
	if err != nil {
		windows.ClosePseudoConsole(hPC)
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		windows.CloseHandle(outWrite)
		return nil, err
	}

	var cwdPtr *uint16
	if cwd != "" {
		cwdPtr, err = windows.UTF16PtrFromString(cwd)
		if err != nil {
			windows.ClosePseudoConsole(hPC)
			windows.CloseHandle(inRead)
			windows.CloseHandle(inWrite)
			windows.CloseHandle(outRead)
			windows.CloseHandle(outWrite)
			return nil, err
		}
	}

	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	var pi windows.ProcessInformation

	err = windows.CreateProcess(
		application,
		cmdLine,
		nil,
		nil,
		false,
		flags,
		&environment[0],
		cwdPtr,
		&si.StartupInfo,
		&pi,
	)

	windows.CloseHandle(inRead)
	windows.CloseHandle(outWrite)

	if err != nil {
		windows.ClosePseudoConsole(hPC)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		return nil, fmt.Errorf("create process with conpty: %w", err)
	}

	terminal := &windowsTerminal{
		hPC:     hPC,
		inWrite: os.NewFile(uintptr(inWrite), "|1"),
		outRead: os.NewFile(uintptr(outRead), "|0"),
		process: pi.Process,
		thread:  pi.Thread,
		done:    make(chan struct{}),
	}
	go terminal.waitForExit()
	return terminal, nil
}

func (t *windowsTerminal) Read(p []byte) (n int, err error) {
	return t.outRead.Read(p)
}

func (t *windowsTerminal) PID() int {
	if t.process == 0 {
		return 0
	}
	id, err := windows.GetProcessId(t.process)
	if err != nil {
		return 0
	}
	return int(id)
}

func (t *windowsTerminal) Write(p []byte) (n int, err error) {
	return t.inWrite.Write(p)
}

func (t *windowsTerminal) Resize(cols, rows int) error {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()
	if t.closed {
		return os.ErrClosed
	}
	return windows.ResizePseudoConsole(t.hPC, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (t *windowsTerminal) Close() error {
	t.closeMu.Lock()
	if t.disposed {
		t.closeMu.Unlock()
		return nil
	}
	t.disposed = true
	t.closed = true
	if !t.exited {
		_ = windows.TerminateProcess(t.process, 1)
	}
	t.closeMu.Unlock()
	if t.inWrite != nil {
		_ = t.inWrite.Close()
	}
	if t.outRead != nil {
		_ = t.outRead.Close()
	}
	select {
	case <-t.done:
		return nil
	case <-time.After(3 * time.Second):
		return fmt.Errorf("conpty cleanup timed out")
	}
}

func (t *windowsTerminal) waitForExit() {
	defer close(t.done)
	_, err := windows.WaitForSingleObject(t.process, windows.INFINITE)
	t.closeMu.Lock()
	t.closed = true
	t.exited = true
	pc := t.hPC
	t.hPC = 0
	t.closeMu.Unlock()
	if err != nil {
		_ = t.outRead.Close()
	}
	// Keep the read side open while conhost flushes its final output. Explicit
	// Close closes that side first, so a failed consumer cannot deadlock conhost.
	windows.ClosePseudoConsole(pc)
	_ = windows.CloseHandle(t.process)
	_ = windows.CloseHandle(t.thread)
}

func windowsEnvironment(env []string) ([]uint16, error) {
	values := map[string]string{}
	for _, entry := range env {
		start := 0
		if strings.HasPrefix(entry, "=") {
			start = 1
		}
		offset := strings.IndexByte(entry[start:], '=')
		if offset < 0 || strings.ContainsRune(entry, 0) {
			return nil, fmt.Errorf("invalid conpty environment")
		}
		name := entry[:offset+start]
		if name == "" {
			return nil, fmt.Errorf("invalid conpty environment name")
		}
		values[strings.ToUpper(name)] = entry
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	block := make([]uint16, 0)
	for _, key := range keys {
		encoded, err := syscall.UTF16FromString(values[key])
		if err != nil {
			return nil, err
		}
		block = append(block, encoded...)
	}
	block = append(block, 0)
	if len(block) == 1 {
		block = append(block, 0)
	}
	return block, nil
}

func findDefaultShell() string {
	for _, candidate := range []string{"pwsh.exe", "powershell.exe", "cmd.exe"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return "cmd.exe"
}
