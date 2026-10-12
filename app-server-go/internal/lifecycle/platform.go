package lifecycle

import "errors"

// ErrLifecycleUnsupported reports that native Astrorder process lifecycle
// operations are unavailable on the current platform.
var ErrLifecycleUnsupported = errors.New("Astrorder native lifecycle helpers require Windows")
