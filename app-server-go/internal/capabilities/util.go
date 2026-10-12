// Package capabilities serves the Capabilities, Skills and RTK settings: it
// reads and edits what the Codex, Hermes and Grok agents have configured (MCP
// servers, plugins, marketplaces, skills) on the local machine, a WSL distro
// or an SSH host. Agents own their config files, so every write keeps the
// rest of the file as it was, keeps a backup, reads the result back and rolls
// back when the read-back does not match.
package capabilities

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

const redactedValue = "[REDACTED]"

var agents = []string{"codex", "hermes", "grok"}

var (
	mcpNameRE    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
	pluginNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}(@[A-Za-z0-9][A-Za-z0-9._-]{0,127})?$`)
	marketNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,127}$`)
	skillNameRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	ansiRE       = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b[@-Z\\\\-_]")
)

func validAgent(agent string) bool {
	for _, name := range agents {
		if name == agent {
			return true
		}
	}
	return false
}

func isSecretKey(key string) bool {
	lower := strings.ToLower(key)
	for _, word := range []string{"secret", "key", "token", "password", "auth", "bearer"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func redactMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		if isSecretKey(key) {
			out[key] = redactedValue
		} else {
			out[key] = value
		}
	}
	return out
}

func redactAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if _, nested := item.(map[string]any); !nested && isSecretKey(key) {
				out[key] = redactedValue
				continue
			}
			out[key] = redactAny(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactAny(item)
		}
		return out
	}
	return value
}

func asMap(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func asString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	}
	return fmt.Sprint(value)
}

func asBool(value any, fallback bool) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return fallback
}

func asStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, asString(item))
		}
		return out
	}
	return []string{}
}

func asStringMap(value any) map[string]string {
	out := map[string]string{}
	if m, ok := value.(map[string]any); ok {
		for key, item := range m {
			out[key] = asString(item)
		}
	}
	return out
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func without(list []string, drop string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != drop {
			out = append(out, item)
		}
	}
	return out
}

func parseYAMLMap(data []byte) (map[string]any, error) {
	out := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func parseConfig(agent string, data []byte) (map[string]any, error) {
	if agent == "hermes" {
		return parseYAMLMap(data)
	}
	return parseTOML(data)
}

func cleanOutput(stdout, stderr string) string {
	text := strings.TrimSpace(ansiRE.ReplaceAllString(stdout, ""))
	if text == "" {
		text = strings.TrimSpace(ansiRE.ReplaceAllString(stderr, ""))
	}
	return text
}

func tail(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	return "…" + text[len(text)-limit:]
}

// notFlag keeps user text from being read as a command-line option.
func notFlag(text string) bool {
	return text != "" && !strings.HasPrefix(text, "-") && !strings.ContainsAny(text, "\r\n\x00")
}
