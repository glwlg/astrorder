package lifecycle

import "errors"

// processAccessError retains the creation time reported by the system process
// snapshot when the process itself cannot be opened.
type processAccessError struct {
	creationTime uint64
	err          error
}

func (e *processAccessError) Error() string { return e.err.Error() }
func (e *processAccessError) Unwrap() error { return e.err }

func processIdentityReused(saved, current ProcessIdentity, inspectErr error) bool {
	creationTime := current.CreationTime
	if inspectErr != nil {
		var accessErr *processAccessError
		if !errors.As(inspectErr, &accessErr) {
			return false
		}
		creationTime = accessErr.creationTime
	}
	return creationTime != 0 && creationTime != saved.CreationTime
}
