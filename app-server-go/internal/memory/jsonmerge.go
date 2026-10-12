package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrTargetFileNotFound = errors.New("target file not found")

func mergeJSONFields(raw []byte, fields map[string]string) ([]byte, error) {
	object := make(map[string]json.RawMessage)
	if len(bytes.TrimSpace(raw)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&object); err != nil {
			return nil, err
		}
	}
	for key, value := range fields {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		object[key] = encoded
	}
	return json.MarshalIndent(object, "", "  ")
}

func verifyJSONFields(raw []byte, expected Config) error {
	var object map[string]json.RawMessage
	if err := decodeSingleJSON(raw, &object); err != nil {
		return errors.New("effective OpenViking config is not valid JSON")
	}
	for key, expectedValue := range map[string]string{"url": expected.URL, "api_key": expected.APIKey} {
		var actual string
		if err := json.Unmarshal(object[key], &actual); err != nil || actual != expectedValue {
			return fmt.Errorf("effective OpenViking field %q does not match", key)
		}
	}
	return nil
}
