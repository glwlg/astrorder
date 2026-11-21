// Package agycredential validates files shared by the App and Session Daemon.
package agycredential

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const MaxBytes = 512 * 1024

// Identity returns an internal account fingerprint. It stays stable across
// token refreshes when AGY includes the account email in the file.
func Identity(data []byte) (string, bool) {
	if len(data) == 0 || len(data) > MaxBytes || !utf8.Valid(data) {
		return "", false
	}
	var file struct {
		Token struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
		} `json:"token"`
		Email string `json:"id_token_email"`
	}
	if json.Unmarshal(data, &file) != nil {
		return "", false
	}
	access, refresh := strings.TrimSpace(file.Token.Access), strings.TrimSpace(file.Token.Refresh)
	if access == "" && refresh == "" {
		return "", false
	}
	var identity string
	switch email := strings.ToLower(strings.TrimSpace(file.Email)); {
	case email != "":
		identity = "email:" + email
	case refresh != "":
		identity = "refresh:" + refresh
	default:
		identity = "access:" + access
	}
	fingerprint := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(fingerprint[:]), true
}
