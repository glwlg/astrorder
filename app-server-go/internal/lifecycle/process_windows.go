//go:build windows

package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

const (
	createNoWindow           = 0x08000000
	createBreakawayFromJob   = 0x01000000
	createNewProcessGroup    = 0x00000200
	processQueryLimited      = 0x1000
	synchronize              = 0x00100000
	waitObject0              = 0x00000000
	waitAbandoned            = 0x00000080
	waitTimeout              = 0x00000102
	errorInsufficientBuffer  = 122
	afINET                   = 2
	afINET6                  = 23
	tcpTableOwnerPIDListener = 3
)

var (
	kernel32Process           = syscall.NewLazyDLL("kernel32.dll")
	iphlpapi                  = syscall.NewLazyDLL("iphlpapi.dll")
	queryFullProcessImageName = kernel32Process.NewProc("QueryFullProcessImageNameW")
	createMutex               = kernel32Process.NewProc("CreateMutexW")
	releaseMutex              = kernel32Process.NewProc("ReleaseMutex")
	getExtendedTCPTable       = iphlpapi.NewProc("GetExtendedTcpTable")
)

// IndependentProcessFlags hides the console, creates a new process group, and
// requests job breakaway without DETACHED_PROCESS.
func IndependentProcessFlags() uint32 {
	return createNoWindow | createBreakawayFromJob | createNewProcessGroup
}

func configureIndependentProcess(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: IndependentProcessFlags(),
	}
	return nil
}

// InspectProcess reads the native executable path and creation timestamp.
func InspectProcess(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, os.ErrNotExist
	}
	handle, err := syscall.OpenProcess(processQueryLimited|synchronize, false, uint32(pid))
	if err != nil {
		if errors.Is(err, syscall.Errno(87)) {
			return ProcessIdentity{}, os.ErrNotExist
		}
		return ProcessIdentity{}, err
	}
	defer syscall.CloseHandle(handle)
	wait, err := syscall.WaitForSingleObject(handle, 0)
	if err != nil {
		return ProcessIdentity{}, err
	}
	if wait == waitObject0 {
		return ProcessIdentity{}, os.ErrNotExist
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	result, _, callErr := queryFullProcessImageName.Call(
		uintptr(handle), 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)),
	)
	if result == 0 {
		return ProcessIdentity{}, callErr
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return ProcessIdentity{}, err
	}
	creationTime := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	return ProcessIdentity{
		PID: pid, ExecutablePath: filepath.Clean(syscall.UTF16ToString(buffer[:size])), CreationTime: creationTime,
	}, nil
}

// ListeningPIDs returns native owner PIDs for exact TCP listeners on port.
func ListeningPIDs(port int) ([]int, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	owners := make(map[int]struct{})
	for _, family := range []uint32{afINET, afINET6} {
		rows, err := tcpListenerRows(family)
		if err != nil {
			return nil, err
		}
		rowSize, portOffset, pidOffset := 24, 8, 20
		if family == afINET6 {
			rowSize, portOffset, pidOffset = 56, 20, 52
		}
		if len(rows) < 4 {
			continue
		}
		count := int(binary.LittleEndian.Uint32(rows[:4]))
		for index := 0; index < count; index++ {
			offset := 4 + index*rowSize
			if offset+rowSize > len(rows) {
				return nil, errors.New("Windows TCP listener table was truncated")
			}
			localPort := int(binary.BigEndian.Uint16(rows[offset+portOffset : offset+portOffset+2]))
			pid := int(binary.LittleEndian.Uint32(rows[offset+pidOffset : offset+pidOffset+4]))
			if localPort == port && pid > 0 {
				owners[pid] = struct{}{}
			}
		}
	}
	result := make([]int, 0, len(owners))
	for pid := range owners {
		result = append(result, pid)
	}
	sort.Ints(result)
	return result, nil
}

func tcpListenerRows(family uint32) ([]byte, error) {
	var size uint32
	code, _, _ := getExtendedTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 1, uintptr(family), tcpTableOwnerPIDListener, 0)
	if code != errorInsufficientBuffer && code != 0 {
		return nil, syscall.Errno(code)
	}
	if size == 0 {
		return []byte{}, nil
	}
	buffer := make([]byte, size)
	code, _, _ = getExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 1, uintptr(family), tcpTableOwnerPIDListener, 0)
	if code != 0 {
		return nil, syscall.Errno(code)
	}
	return buffer[:size], nil
}

func acquireLifecycleLock(ctx context.Context, stateDir string) (func(), error) {
	runtime.LockOSThread()
	lockedThread := true
	defer func() {
		if lockedThread {
			runtime.UnlockOSThread()
		}
	}()
	canonicalState, _, err := resolveExistingPath(stateDir)
	if err != nil {
		return nil, fmt.Errorf("resolve lifecycle state directory: %w", err)
	}
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(canonicalState))))
	name, err := syscall.UTF16PtrFromString(fmt.Sprintf("Local\\AstrorderAppLifecycle-%x", digest[:16]))
	if err != nil {
		return nil, err
	}
	handle, _, callErr := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, callErr
	}
	for {
		result, err := syscall.WaitForSingleObject(syscall.Handle(handle), 100)
		if err != nil {
			syscall.CloseHandle(syscall.Handle(handle))
			return nil, err
		}
		if result == waitObject0 || result == waitAbandoned {
			lockedThread = false
			return func() {
				releaseMutex.Call(handle)
				syscall.CloseHandle(syscall.Handle(handle))
				runtime.UnlockOSThread()
			}, nil
		}
		if result != waitTimeout {
			syscall.CloseHandle(syscall.Handle(handle))
			return nil, fmt.Errorf("unexpected lifecycle mutex wait result %#x", result)
		}
		select {
		case <-ctx.Done():
			syscall.CloseHandle(syscall.Handle(handle))
			return nil, ctx.Err()
		default:
		}
	}
}
