package sessions

import (
	"regexp"
	"strings"
)

var (
	hermesMemoryRegex   = regexp.MustCompile(`(?sm)(?:\r?\n)*<memory-context>\r?\n\[System note: The following is recalled memory context, NOT new user input\.[^\r\n]*\]\r?\n.*?^</memory-context>(?:\r?\n)*`)
	hermesPonytailRegex = regexp.MustCompile(`(?s)(?:\r?\n)*PONYTAIL MODE ACTIVE — level: (?:lite|full|ultra)\r?\n\r?\n# Ponytail\r?\n.*?\r?\nThe shortest path to done is the right path\.\s*\z`)
)

func visibleHandoffUserText(text string) string {
	marker := "\n<用户的新消息>\n"
	if idx := strings.LastIndex(text, marker); idx != -1 {
		return text[idx+len(marker):]
	}
	return text
}

func visibleHermesUserText(text string) string {
	text = hermesPonytailRegex.ReplaceAllString(text, "")
	if strings.Contains(text, "<memory-context>") {
		text = hermesMemoryRegex.ReplaceAllString(text, "\n\n")
	}
	return strings.TrimSpace(text)
}
