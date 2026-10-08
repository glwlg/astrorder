//go:build windows

package process

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type CommandOwner struct {
	mu     sync.Mutex
	job    windows.Handle
	pid    int
	closed bool
	err    error
}
type jobBasicAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

func StartOwnedCommand(cmd *exec.Cmd) (owner *CommandOwner, err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			windows.CloseHandle(job)
		}
	}()
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	// The suspended leader cannot create children before it is assigned to its
	// job. Fail closed and reap it if any ownership/resume operation fails.
	started := false
	defer func() {
		if !started {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)
	if err = windows.AssignProcessToJobObject(job, handle); err != nil {
		return nil, err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	err = windows.Thread32First(snapshot, &entry)
	found := false
	for err == nil {
		if entry.OwnerProcessID == uint32(cmd.Process.Pid) {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return nil, openErr
			}
			_, resumeErr := windows.ResumeThread(thread)
			windows.CloseHandle(thread)
			if resumeErr != nil {
				return nil, resumeErr
			}
			found = true
			break
		}
		err = windows.Thread32Next(snapshot, &entry)
	}
	if !found {
		return nil, fmt.Errorf("owned process primary thread was not found")
	}
	started = true
	success = true
	return &CommandOwner{job: job, pid: cmd.Process.Pid}, nil
}

func (o *CommandOwner) PID() int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pid
}
func (o *CommandOwner) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return o.err
	}
	o.closed = true
	err := windows.TerminateJobObject(o.job, 1)
	deadline := time.Now().Add(5 * time.Second)
	for err == nil {
		var info jobBasicAccounting
		queryErr := windows.QueryInformationJobObject(o.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
		if queryErr != nil {
			err = queryErr
			break
		}
		if info.ActiveProcesses == 0 {
			break
		}
		if time.Now().After(deadline) {
			err = fmt.Errorf("owned process tree did not exit")
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	o.err = errors.Join(err, windows.CloseHandle(o.job))
	return o.err
}
