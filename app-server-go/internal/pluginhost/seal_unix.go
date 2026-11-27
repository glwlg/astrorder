//go:build !windows

package pluginhost

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	aesGCMPrefix = "aesgcm:"
	keyByteLen   = 32
	keyFileName  = ".ext-secrets.key"
)

type secretSealer struct {
	mu      sync.Mutex
	keyDir  string
	memKey  []byte
}

func newSecretSealer(keyDir string) *secretSealer {
	return &secretSealer{keyDir: strings.TrimSpace(keyDir)}
}

func (s *secretSealer) aead(create bool) (cipher.AEAD, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, err := s.loadOrCreateKeyLocked(create)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (s *secretSealer) loadOrCreateKeyLocked(create bool) ([]byte, error) {
	if len(s.memKey) == keyByteLen {
		return s.memKey, nil
	}
	if s.keyDir == "" {
		if !create {
			return nil, errors.New("plugin secret key is unavailable")
		}
		key := make([]byte, keyByteLen)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		s.memKey = key
		return key, nil
	}

	keyPath := filepath.Join(s.keyDir, keyFileName)
	data, err := os.ReadFile(keyPath)
	if err == nil && len(data) == keyByteLen {
		s.memKey = data
		return data, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read plugin secret key: %w", err)
	}
	if !create {
		return nil, errors.New("plugin secret key not found")
	}

	if err := os.MkdirAll(s.keyDir, 0o700); err != nil {
		return nil, fmt.Errorf("create secret key dir: %w", err)
	}
	key := make([]byte, keyByteLen)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(s.keyDir, ".ext-secrets.key.tmp-*")
	if err != nil {
		return nil, fmt.Errorf("create temp secret key: %w", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return nil, err
	}
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := os.Link(tmpName, keyPath); err != nil {
		_ = os.Remove(tmpName)
		if errors.Is(err, os.ErrExist) {
			existing, readErr := os.ReadFile(keyPath)
			if readErr == nil && len(existing) == keyByteLen {
				s.memKey = existing
				return existing, nil
			}
		}
		return nil, fmt.Errorf("persist plugin secret key: %w", err)
	}
	_ = os.Remove(tmpName)
	s.memKey = key
	return key, nil
}

func (s *secretSealer) SealString(plaintext string) (string, error) {
	box, err := s.aead(true)
	if err != nil {
		return "", fmt.Errorf("seal plugin secret: %w", err)
	}
	sealed := box.Seal(nil, nil, []byte(plaintext), nil)
	return aesGCMPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *secretSealer) UnsealString(sealed string) (string, error) {
	if !strings.HasPrefix(sealed, aesGCMPrefix) {
		return "", fmt.Errorf("unexpected secret encoding")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, aesGCMPrefix))
	if err != nil {
		return "", fmt.Errorf("decode plugin secret: %w", err)
	}
	box, err := s.aead(false)
	if err != nil {
		return "", fmt.Errorf("unseal plugin secret: %w", err)
	}
	plain, err := box.Open(nil, nil, raw, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt plugin secret: %w", err)
	}
	return string(plain), nil
}
