//go:build windows

package pluginhost

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pluginProcessGuard owns a Windows Job Object configured with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so the entire plugin process tree is
// automatically reclaimed by the OS if the host exits or crashes.
type pluginProcessGuard struct {
	mu        sync.Mutex
	job       windows.Handle
	closeOnce sync.Once
	closed    bool
}

func configurePluginProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

func startPluginProcess(cmd *exec.Cmd) (guard *pluginProcessGuard, err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create windows job object: %w", err)
	}
	defer func() {
		if err != nil {
			_ = windows.CloseHandle(job)
		}
	}()

	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		return nil, fmt.Errorf("configure job kill-on-close limit: %w", err)
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW

	if err = cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	procHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, fmt.Errorf("open plugin process for job assignment: %w", err)
	}
	defer windows.CloseHandle(procHandle)

	if err = windows.AssignProcessToJobObject(job, procHandle); err != nil {
		return nil, fmt.Errorf("assign plugin process to job object: %w", err)
	}

	if err = resumePrimaryThread(uint32(cmd.Process.Pid)); err != nil {
		return nil, err
	}

	return &pluginProcessGuard{job: job}, nil
}

func resumePrimaryThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot threads for plugin process: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			return fmt.Errorf("open plugin primary thread: %w", openErr)
		}
		_, resumeErr := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if resumeErr != nil {
			return fmt.Errorf("resume plugin primary thread: %w", resumeErr)
		}
		return nil
	}
	return fmt.Errorf("plugin process primary thread unavailable: %w", err)
}

func (g *pluginProcessGuard) Terminate(cmd *exec.Cmd) {
	if g != nil {
		g.mu.Lock()
		if !g.closed && g.job != 0 {
			_ = windows.TerminateJobObject(g.job, 1)
			g.mu.Unlock()
			return
		}
		g.mu.Unlock()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (g *pluginProcessGuard) ForceKill(cmd *exec.Cmd) {
	if g != nil {
		g.mu.Lock()
		if !g.closed && g.job != 0 {
			_ = windows.TerminateJobObject(g.job, 1)
		}
		g.mu.Unlock()
		_ = g.Release()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (g *pluginProcessGuard) Release() error {
	if g == nil {
		return nil
	}
	var closeErr error
	g.closeOnce.Do(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.closed = true
		if g.job != 0 {
			closeErr = windows.CloseHandle(g.job)
			g.job = 0
		}
	})
	return closeErr
}
