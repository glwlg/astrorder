package runtime

// RejectedBeforeDispatch marks a local validation failure with no native IO.
// Never use it for RPC errors or failures after submission has begun.
type RejectedBeforeDispatch struct{ Message string }

func (e *RejectedBeforeDispatch) Error() string { return e.Message }
func RejectBeforeDispatch(message string) error { return &RejectedBeforeDispatch{Message: message} }
