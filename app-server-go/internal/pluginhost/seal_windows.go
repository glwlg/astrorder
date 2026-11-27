//go:build windows

package pluginhost

import (
	"encoding/base64"
	"fmt"
	"strings"

	"astrorder.dev/app-server/internal/credentials"
)

const dpapiPrefix = "dpapi:"

type secretSealer struct{}

func newSecretSealer(_ string) *secretSealer {
	return &secretSealer{}
}

func (s *secretSealer) SealString(plaintext string) (string, error) {
	cipherBytes, err := credentials.Encrypt([]byte(plaintext))
	if err != nil {
		return "", fmt.Errorf("encrypt plugin secret: %w", err)
	}
	return dpapiPrefix + base64.StdEncoding.EncodeToString(cipherBytes), nil
}

func (s *secretSealer) UnsealString(sealed string) (string, error) {
	if !strings.HasPrefix(sealed, dpapiPrefix) {
		return "", fmt.Errorf("unexpected secret encoding")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, dpapiPrefix))
	if err != nil {
		return "", fmt.Errorf("decode plugin secret: %w", err)
	}
	plain, err := credentials.Decrypt(raw)
	if err != nil {
		return "", fmt.Errorf("decrypt plugin secret: %w", err)
	}
	return string(plain), nil
}
