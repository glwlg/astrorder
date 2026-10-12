//go:build windows

package lifecycle

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const sddlRevision1 = 1

var (
	advapi32ACL                     = syscall.NewLazyDLL("advapi32.dll")
	convertStringSecurityDescriptor = advapi32ACL.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	setFileSecurity                 = advapi32ACL.NewProc("SetFileSecurityW")
	localFreeACL                    = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("private directory must not be a reparse point")
	}
	resolved, err := canonicalExistingPath(path)
	if err != nil || !samePath(path, resolved) {
		return fmt.Errorf("private directory path must be canonical and must not traverse a reparse point")
	}
	return protectPathACL(path, true)
}

func privateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("private file must be a regular file")
	}
	resolved, err := canonicalExistingPath(path)
	if err != nil || !samePath(path, resolved) {
		return fmt.Errorf("private file path must be canonical and must not traverse a reparse point")
	}
	return protectPathACL(path, false)
}

func protectPathACL(path string, inheritable bool) error {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	userSID, err := user.User.Sid.String()
	if err != nil {
		return err
	}
	// Protected DACL: current user and LocalSystem only. Directories propagate
	// the two full-control ACEs to children; files receive explicit ACEs.
	aceFlags := ""
	if inheritable {
		aceFlags = "OICI"
	}
	sddl, err := syscall.UTF16PtrFromString("D:PAI(A;" + aceFlags + ";FA;;;" + userSID + ")(A;" + aceFlags + ";FA;;;SY)")
	if err != nil {
		return err
	}
	var descriptor uintptr
	result, _, callErr := convertStringSecurityDescriptor.Call(
		uintptr(unsafe.Pointer(sddl)), sddlRevision1, uintptr(unsafe.Pointer(&descriptor)), 0,
	)
	if result == 0 {
		return fmt.Errorf("create private directory security descriptor: %w", callErr)
	}
	defer localFreeACL.Call(descriptor)
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// DACL_SECURITY_INFORMATION = 0x4; PROTECTED_DACL_SECURITY_INFORMATION = 0x80000000.
	result, _, callErr = setFileSecurity.Call(
		uintptr(unsafe.Pointer(pathPointer)), 0x80000004, descriptor,
	)
	if result == 0 {
		return fmt.Errorf("protect private directory ACL: %w", callErr)
	}
	return nil
}
