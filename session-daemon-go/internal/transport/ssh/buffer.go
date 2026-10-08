package ssh

import "sync"

// BoundedBuffer implements a thread-safe byte buffer capped at a maximum size.
// If writes exceed the capacity, additional bytes are discarded without error.
type BoundedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

// NewBoundedBuffer creates a new BoundedBuffer with the given limit in bytes.
func NewBoundedBuffer(limit int) *BoundedBuffer {
	if limit <= 0 {
		limit = 64 * 1024
	}
	return &BoundedBuffer{limit: limit}
}

// Write appends bytes up to the configured limit, discarding the rest.
func (b *BoundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	remaining := b.limit - len(b.buf)
	if remaining > 0 {
		if len(p) <= remaining {
			b.buf = append(b.buf, p...)
		} else {
			b.buf = append(b.buf, p[:remaining]...)
			b.truncated = true
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}

// Bytes returns a copy of the buffered bytes.
func (b *BoundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	return out
}

// Truncated returns true if any writes were truncated due to exceeding capacity.
func (b *BoundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
