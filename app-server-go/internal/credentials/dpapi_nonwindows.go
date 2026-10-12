//go:build !windows

package credentials

// Supported reports that current-user Windows DPAPI is unavailable.
func Supported() bool { return false }

// Encrypt never substitutes another cipher for current-user Windows DPAPI.
func Encrypt([]byte) ([]byte, error) { return nil, ErrUnsupportedPlatform }

// Decrypt never treats a DPAPI blob as plaintext on unsupported platforms.
func Decrypt([]byte) ([]byte, error) { return nil, ErrUnsupportedPlatform }
