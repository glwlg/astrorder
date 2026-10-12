package parity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// decodeStrictObject decodes raw JSON into target strictly:
// - raw is trimmed and must start with '{' (strict JSON object)
// - disallows unknown fields in target
// - ensures there is no trailing garbage after the object
func decodeStrictObject(raw []byte, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return errors.New("empty JSON object")
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("expected JSON object starting with '{', got '%c'", trimmed[0])
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing content after JSON object")
	}
	return nil
}
