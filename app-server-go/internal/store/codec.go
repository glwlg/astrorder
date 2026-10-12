package store

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var jsonColumns = map[string]bool{
	"attachments":   true,
	"baseline_ids":  true,
	"capabilities":  true,
	"data":          true,
	"details":       true,
	"logs":          true,
	"progress":      true,
	"runtime_owner": true,
	"tool":          true,
	"value":         true,
}

func isJSONColumn(name string) bool { return jsonColumns[name] }

func decodeJSON(value any) (any, error) {
	var raw []byte
	switch typed := value.(type) {
	case string:
		raw = []byte(typed)
	case []byte:
		raw = typed
	default:
		return nil, fmt.Errorf("expected JSON text, got %T", value)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return normalizeNumbers(decoded), nil
}

func normalizeNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = normalizeNumbers(typed[i])
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeNumbers(item)
		}
	}
	return value
}

func encodeJSON(value any) (string, error) {
	raw, err := marshalJSON(value, false)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// marshalJSON keeps native json.Number lexemes and disables Go's HTML escaping.
// Python's ensure_ascii=False also emits U+2028/U+2029 literally, so replace the
// two escapes that encoding/json retains for JavaScript compatibility.
func marshalJSON(value any, pythonCompatible bool) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	if pythonCompatible {
		raw = unescapeJSONSeparators(raw)
	}
	return raw, nil
}

// Decode only actual JSON Unicode escapes. Paired backslashes encode literal
// source text such as "\\u2028" and must survive canonical hashing unchanged.
func unescapeJSONSeparators(raw []byte) []byte {
	result := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		if raw[i] != '\\' || i+1 == len(raw) {
			result = append(result, raw[i])
			i++
			continue
		}
		if i+6 <= len(raw) && raw[i+1] == 'u' {
			switch string(raw[i+2 : i+6]) {
			case "2028":
				result = append(result, []byte("\u2028")...)
				i += 6
				continue
			case "2029":
				result = append(result, []byte("\u2029")...)
				i += 6
				continue
			}
		}
		result = append(result, raw[i:i+2]...)
		i += 2
	}
	return result
}

func parseTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), nil
	case string:
		if typed == "" {
			return time.Time{}, errors.New("empty timestamp")
		}
		layouts := []string{
			time.RFC3339Nano,
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
			// Legacy rows written before the re-upsert preservation bug was
			// fixed carry Go's time.Time.String() form; reads must tolerate
			// them or one bad row fails the whole endpoint (for example
			// presence) for every session in the database.
			"2006-01-02 15:04:05.999999999 -0700 MST",
		}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, typed); err == nil {
				return parsed.UTC(), nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %v", value)
}

func databaseTime(value any) (string, error) {
	parsed, err := parseTime(value)
	if err != nil {
		return "", err
	}
	return parsed.Format("2006-01-02 15:04:05.000000"), nil
}

func nowDatabaseTime() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05.000000")
}

func wireTime(value any) (string, error) {
	parsed, err := parseTime(value)
	if err != nil {
		return "", err
	}
	return parsed.Format("2006-01-02T15:04:05.000Z"), nil
}

func integer(value any, fallback int64) (int64, error) {
	if value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case uint:
		if uint64(typed) > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("integer overflow")
		}
		return int64(typed), nil
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("integer overflow")
		}
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	case float64:
		if typed != float64(int64(typed)) {
			return 0, fmt.Errorf("not an integer: %v", typed)
		}
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("not an integer: %T", value)
	}
}

func boolValue(value any, fallback bool) (bool, error) {
	if value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case int:
		return typed != 0, nil
	case int64:
		return typed != 0, nil
	default:
		return false, fmt.Errorf("not a boolean: %T", value)
	}
}

func stringValue(payload map[string]any, key string, required bool) (string, error) {
	value, ok := payload[key]
	if !ok || value == nil {
		if required {
			return "", fmt.Errorf("%s is required", key)
		}
		return "", nil
	}
	text, ok := value.(string)
	if !ok || (required && text == "") {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return text, nil
}

func randomID(prefix string, bytes int) (string, error) {
	if bytes < 1 {
		return "", errors.New("random ID size must be positive")
	}
	raw := make([]byte, (bytes+1)/2)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw)[:bytes], nil
}

func payloadHash(payload map[string]any) (string, error) {
	raw, err := marshalJSON(payload, true)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func encodeCursor(rowID int64) string {
	raw := []byte(fmt.Sprintf(`{"row":%d}`, rowID))
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(cursor string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, errors.New("invalid history cursor")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return 0, errors.New("invalid history cursor")
	}
	rowID, err := integer(value["row"], 0)
	if err != nil || rowID < 1 {
		return 0, errors.New("invalid history cursor")
	}
	return rowID, nil
}

var sensitiveText = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|authorization|private[_ -]?key)\s*[:=]\s*[^\s,;]+`)
var sensitiveKey = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|authorization|private[_ -]?key|credential|connection[_-]?string)`)

func redactText(value string) string {
	value = sensitiveText.ReplaceAllString(value, "$1=[REDACTED]")
	value = strings.NewReplacer("\x00", " ", "\r", " ", "\n", " ").Replace(value)
	return truncateCodepoints(strings.TrimSpace(value), 2000)
}

func truncateCodepoints(value string, limit int) string {
	if limit < 1 || len(value) == 0 {
		if limit < 1 {
			return ""
		}
		return value
	}
	count := 0
	for byteIndex := range value {
		if count == limit {
			return value[:byteIndex]
		}
		count++
	}
	return value
}

func redactStructured(value any, depth int) any {
	if depth > 16 {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if sensitiveKey.MatchString(key) {
				result[key] = "[REDACTED]"
			} else {
				result[key] = redactStructured(typed[key], depth+1)
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = redactStructured(typed[i], depth+1)
		}
		return result
	case string:
		return redactText(typed)
	default:
		return value
	}
}

// WireTime formats a stored timestamp the way the HTTP API returns it.
func WireTime(value any) (string, error) { return wireTime(value) }

// ParseTime parses every timestamp layout the store has ever written.
func ParseTime(value any) (time.Time, error) { return parseTime(value) }
