package ssh

import "strings"

// Quote safely escapes a string for POSIX-compliant shells by enclosing it in
// single quotes and escaping internal single quotes via '\''.
// Empty strings are returned as ''. This guarantees literal preservation of
// spaces, quotes, newlines, variables, glob characters, and Unicode bytes.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
