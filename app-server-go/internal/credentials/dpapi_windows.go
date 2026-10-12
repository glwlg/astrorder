//go:build windows

package credentials

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

const cryptProtectUIForbidden = 0x1

type dataBlob struct {
	size uint32
	data *byte
}

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	cryptProtectData   = crypt32.NewProc("CryptProtectData")
	cryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	localFree          = kernel32.NewProc("LocalFree")
)

// Supported reports whether this build provides current-user Windows DPAPI.
func Supported() bool { return true }

// Encrypt protects plaintext with Windows DPAPI for the current user.
func Encrypt(plaintext []byte) ([]byte, error) {
	return transform(cryptProtectData, plaintext, "encrypt")
}

// Decrypt unprotects current-user Windows DPAPI ciphertext.
func Decrypt(ciphertext []byte) ([]byte, error) {
	return transform(cryptUnprotectData, ciphertext, "decrypt")
}

func transform(operation *syscall.LazyProc, input []byte, verb string) ([]byte, error) {
	copyOfInput := append([]byte(nil), input...)
	in := dataBlob{size: uint32(len(copyOfInput))}
	if len(copyOfInput) > 0 {
		in.data = &copyOfInput[0]
	}
	var out dataBlob
	result, _, callErr := operation.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	runtime.KeepAlive(copyOfInput)
	if result == 0 {
		if callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return nil, fmt.Errorf("DPAPI %s: %w", verb, callErr)
	}
	if out.data == nil {
		return []byte{}, nil
	}
	defer localFree.Call(uintptr(unsafe.Pointer(out.data)))
	return append([]byte(nil), unsafe.Slice(out.data, int(out.size))...), nil
}
