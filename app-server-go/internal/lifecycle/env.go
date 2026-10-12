package lifecycle

import (
	"os"
	"runtime"
	"sort"
	"strings"
)

// ProcessEnvironment builds a sanitized child process environment where configured
// ASTRORDER_* values replace inherited environment variables.
func ProcessEnvironment(values map[string]string) []string {
	result := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		normalized := key
		if runtime.GOOS == "windows" {
			normalized = strings.ToUpper(key)
		}
		if !strings.HasPrefix(normalized, "ASTRORDER_") {
			result = append(result, entry)
		}
	}
	normalizedValues := make(map[string]string, len(values))
	inputKeys := make([]string, 0, len(values))
	for key := range values {
		inputKeys = append(inputKeys, key)
	}
	sort.Strings(inputKeys)
	for _, key := range inputKeys {
		normalized := key
		if runtime.GOOS == "windows" {
			normalized = strings.ToUpper(key)
		}
		if _, exists := normalizedValues[normalized]; exists {
			continue
		}
		normalizedValues[normalized] = values[key]
	}
	keys := make([]string, 0, len(normalizedValues))
	for key := range normalizedValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+normalizedValues[key])
	}
	return result
}
