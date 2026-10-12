package memory

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	tomlSectionPattern = regexp.MustCompile(`^\s*\[([^]]+)\]\s*(?:#.*)?$`)
	tomlKeyPattern     = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=`)
)

func ensureTOMLBoolean(raw []byte, section, key string, value bool) ([]byte, error) {
	if bytes.IndexByte(raw, 0) >= 0 {
		return nil, errors.New("Codex config contains NUL bytes")
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	sectionStart, sectionEnd := -1, len(lines)
	for index, line := range lines {
		match := tomlSectionPattern.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		if sectionStart >= 0 {
			sectionEnd = index
			break
		}
		if match[1] == section {
			sectionStart = index
		}
	}
	wanted := fmt.Sprintf("%s = %t", key, value)
	if sectionStart < 0 {
		if strings.TrimSpace(text) != "" && !strings.HasSuffix(text, "\n\n") {
			if strings.HasSuffix(text, "\n") {
				text += "\n"
			} else {
				text += "\n\n"
			}
		}
		return []byte(text + "[" + section + "]\n" + wanted + "\n"), nil
	}
	for index := sectionStart + 1; index < sectionEnd; index++ {
		match := tomlKeyPattern.FindStringSubmatch(lines[index])
		if len(match) == 2 && match[1] == key {
			lines[index] = wanted
			return []byte(strings.Join(lines, "\n")), nil
		}
	}
	lines = append(lines[:sectionEnd], append([]string{wanted}, lines[sectionEnd:]...)...)
	return []byte(strings.Join(lines, "\n")), nil
}

func hasTOMLBoolean(raw []byte, section, key string, value bool) bool {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	inSection := false
	wanted := fmt.Sprintf("%t", value)
	for _, line := range lines {
		if match := tomlSectionPattern.FindStringSubmatch(line); len(match) == 2 {
			inSection = match[1] == section
			continue
		}
		if !inSection {
			continue
		}
		if match := tomlKeyPattern.FindStringSubmatch(line); len(match) == 2 && match[1] == key {
			parts := strings.SplitN(line, "=", 2)
			return strings.TrimSpace(strings.SplitN(parts[1], "#", 2)[0]) == wanted
		}
	}
	return false
}
