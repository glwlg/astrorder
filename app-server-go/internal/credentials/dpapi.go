package credentials

import "errors"

// ErrUnsupportedPlatform is returned when current-user Windows DPAPI is not
// available. Callers must not fall back to plaintext or a substitute cipher.
var ErrUnsupportedPlatform = errors.New("current-user Windows DPAPI is unsupported on this platform")
