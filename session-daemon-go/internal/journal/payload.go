package journal

import (
	"bytes"
	"encoding/json"
)

// DecodePayload preserves opaque native numeric identifiers through persistence.
func DecodePayload(raw []byte, payload *map[string]any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(payload)
}
