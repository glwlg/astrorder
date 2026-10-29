//go:build windows

package audio

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The App owns this job handle: Windows reclaims the model tree even if the App
// is terminated before its Go shutdown handlers can run.
func startPlatformProcess(cmd *exec.Cmd) (release func() error, err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, err
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
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(job, process); err != nil {
		return nil, err
	}
	threads, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(threads)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(threads, &entry); err == nil; err = windows.Thread32Next(threads, &entry) {
		if entry.OwnerProcessID != uint32(cmd.Process.Pid) {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			return nil, openErr
		}
		_, err = windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if err != nil {
			return nil, err
		}
		return func() error { return windows.CloseHandle(job) }, nil
	}
	return nil, fmt.Errorf("语音进程主线程不可用: %w", err)
}

const (
	createNoWindow = 0x08000000
)

func configurePlatformProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	cmd.Env = sanitizedEnv()
}

func sanitizedEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		// Strip sensitive credentials, tokens, secrets, and auth headers
		upper := strings.ToUpper(entry)
		key, _, _ := strings.Cut(upper, "=")
		// An empty visibility mask hides all Vulkan devices in llama.cpp.
		if key == "GGML_VK_VISIBLE_DEVICES" && strings.HasSuffix(entry, "=") {
			continue
		}
		if strings.Contains(key, "SECRET") ||
			strings.Contains(key, "CREDENTIAL") ||
			strings.Contains(key, "TOKEN") ||
			strings.Contains(key, "PASSWORD") ||
			strings.Contains(key, "AUTH") ||
			strings.HasPrefix(key, "ASTRORDER_") {
			continue
		}
		env = append(env, entry)
	}
	return env
}
