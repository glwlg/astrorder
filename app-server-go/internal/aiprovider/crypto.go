package aiprovider

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"astrorder.dev/app-server/internal/store"
)

const (
	// SecretCipherPrefix marks AES-256-GCM encrypted secrets stored in SQLite.
	SecretCipherPrefix = "enc:v1:"
	masterKeyFileName  = "aiprovider.key"
	masterKeySize      = 32
)

var (
	// ErrInvalidCiphertext indicates malformed versioned ciphertext framing or encoding.
	ErrInvalidCiphertext = errors.New("aiprovider: invalid encrypted secret")
	// ErrDecryptSecret indicates AES-256-GCM decryption or authentication failed.
	ErrDecryptSecret = errors.New("aiprovider: failed to decrypt secret")

	keyFileMu sync.Mutex
)

// IsEncryptedSecret reports whether stored carries the versioned ciphertext prefix.
func IsEncryptedSecret(stored string) bool {
	return strings.HasPrefix(strings.TrimSpace(stored), SecretCipherPrefix)
}

func masterKeyPath(st *store.Store) (string, error) {
	if st == nil {
		return "", errors.New("aiprovider: store unavailable for master key")
	}
	dbPath := strings.TrimSpace(st.Path())
	if dbPath == "" {
		return "", errors.New("aiprovider: store path is empty")
	}
	return filepath.Join(filepath.Dir(dbPath), masterKeyFileName), nil
}

func loadMasterKey(st *store.Store, createIfMissing bool) ([]byte, error) {
	keyPath, err := masterKeyPath(st)
	if err != nil {
		return nil, err
	}
	keyFileMu.Lock()
	defer keyFileMu.Unlock()

	key, err := readMasterKeyFile(keyPath)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !createIfMissing {
		return nil, fmt.Errorf("%w: master key file %s not found", ErrDecryptSecret, masterKeyFileName)
	}
	return createMasterKeyFile(keyPath)
}

func readMasterKeyFile(keyPath string) ([]byte, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	if len(raw) != masterKeySize {
		return nil, fmt.Errorf("%w: invalid master key length %d", ErrDecryptSecret, len(raw))
	}
	out := make([]byte, masterKeySize)
	copy(out, raw)
	return out, nil
}

func createMasterKeyFile(keyPath string) ([]byte, error) {
	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("aiprovider: create master key directory: %w", err)
	}
	key := make([]byte, masterKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("aiprovider: generate master key: %w", err)
	}

	var suffix [6]byte
	_, _ = io.ReadFull(rand.Reader, suffix[:])
	tmpPath := filepath.Join(dir, "."+masterKeyFileName+".tmp."+hex.EncodeToString(suffix[:]))

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("aiprovider: create temp master key file: %w", err)
	}
	_, writeErr := f.Write(key)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		if writeErr != nil {
			return nil, fmt.Errorf("aiprovider: write temp master key file: %w", writeErr)
		}
		if syncErr != nil {
			return nil, fmt.Errorf("aiprovider: sync temp master key file: %w", syncErr)
		}
		return nil, fmt.Errorf("aiprovider: close temp master key file: %w", closeErr)
	}
	_ = os.Chmod(tmpPath, 0o600)
	defer func() { _ = os.Remove(tmpPath) }()

	// Hard link atomically creates keyPath without overwriting an existing key
	// created concurrently by another process.
	if err := os.Link(tmpPath, keyPath); err == nil {
		_ = os.Chmod(keyPath, 0o600)
		return key, nil
	} else if errors.Is(err, os.ErrExist) {
		return readMasterKeyFile(keyPath)
	}

	// Fallback for filesystems that do not support hard links.
	dst, err := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return readMasterKeyFile(keyPath)
		}
		return nil, fmt.Errorf("aiprovider: persist master key file: %w", err)
	}
	_, writeErr = dst.Write(key)
	syncErr = dst.Sync()
	closeErr = dst.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(keyPath)
		if writeErr != nil {
			return nil, fmt.Errorf("aiprovider: write master key file: %w", writeErr)
		}
		if syncErr != nil {
			return nil, fmt.Errorf("aiprovider: sync master key file: %w", syncErr)
		}
		return nil, fmt.Errorf("aiprovider: close master key file: %w", closeErr)
	}
	_ = os.Chmod(keyPath, 0o600)
	return key, nil
}

// EncryptSecret encrypts plaintext with AES-256-GCM using the store's data-dir master key.
// Empty strings remain empty.
func EncryptSecret(st *store.Store, plaintext string) (string, error) {
	trimmed := strings.TrimSpace(plaintext)
	if trimmed == "" {
		return "", nil
	}
	key, err := loadMasterKey(st, true)
	if err != nil {
		return "", err
	}
	return encryptSecretWithKey(key, trimmed)
}

// DecryptSecret decrypts a stored secret using the store's master key.
// Legacy values without the enc:v1: prefix are returned directly as plaintext
// with wasEncrypted == false for backward compatibility.
func DecryptSecret(st *store.Store, stored string) (plaintext string, wasEncrypted bool, err error) {
	trimmed := strings.TrimSpace(stored)
	if trimmed == "" {
		return "", false, nil
	}
	if !strings.HasPrefix(trimmed, SecretCipherPrefix) {
		return trimmed, false, nil
	}
	key, err := loadMasterKey(st, false)
	if err != nil {
		return "", true, err
	}
	return decryptSecretWithKey(key, trimmed)
}

func encryptSecretWithKey(key []byte, plaintext string) (string, error) {
	trimmed := strings.TrimSpace(plaintext)
	if trimmed == "" {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aiprovider: init aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("aiprovider: init gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("aiprovider: generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(trimmed), nil)
	return SecretCipherPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func decryptSecretWithKey(key []byte, stored string) (string, bool, error) {
	trimmed := strings.TrimSpace(stored)
	if trimmed == "" {
		return "", false, nil
	}
	if !strings.HasPrefix(trimmed, SecretCipherPrefix) {
		return trimmed, false, nil
	}
	encoded := strings.TrimPrefix(trimmed, SecretCipherPrefix)
	if encoded == "" {
		return "", true, fmt.Errorf("%w: empty payload", ErrInvalidCiphertext)
	}
	blob, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", true, fmt.Errorf("%w: invalid base64: %v", ErrInvalidCiphertext, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", true, fmt.Errorf("aiprovider: init aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", true, fmt.Errorf("aiprovider: init gcm: %w", err)
	}
	if len(blob) < gcm.NonceSize()+gcm.Overhead() {
		return "", true, fmt.Errorf("%w: ciphertext truncated", ErrInvalidCiphertext)
	}
	nonce := blob[:gcm.NonceSize()]
	ciphertext := blob[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", true, fmt.Errorf("%w: %v", ErrDecryptSecret, err)
	}
	return string(plain), true, nil
}

func encryptConfigForStorage(st *store.Store, cfg Config) (Config, error) {
	out := cfg
	out.Providers = make([]Provider, len(cfg.Providers))
	copy(out.Providers, cfg.Providers)

	needsKey := false
	for _, p := range out.Providers {
		if strings.TrimSpace(p.APIKey) != "" || strings.TrimSpace(p.ManagementKey) != "" {
			needsKey = true
			break
		}
	}
	if !needsKey {
		return out, nil
	}

	key, err := loadMasterKey(st, true)
	if err != nil {
		return Config{}, err
	}
	for i := range out.Providers {
		if raw := strings.TrimSpace(out.Providers[i].APIKey); raw != "" {
			enc, err := encryptSecretWithKey(key, raw)
			if err != nil {
				return Config{}, fmt.Errorf("encrypt provider %s api_key: %w", out.Providers[i].ID, err)
			}
			out.Providers[i].APIKey = enc
		}
		if raw := strings.TrimSpace(out.Providers[i].ManagementKey); raw != "" {
			enc, err := encryptSecretWithKey(key, raw)
			if err != nil {
				return Config{}, fmt.Errorf("encrypt provider %s management_key: %w", out.Providers[i].ID, err)
			}
			out.Providers[i].ManagementKey = enc
		}
	}
	return out, nil
}

func decryptConfigFromStorage(st *store.Store, saved Config) (Config, bool, error) {
	out := saved
	out.Providers = make([]Provider, len(saved.Providers))
	copy(out.Providers, saved.Providers)

	hasEncrypted := false
	needsMigration := false
	for _, p := range out.Providers {
		for _, secret := range []string{strings.TrimSpace(p.APIKey), strings.TrimSpace(p.ManagementKey)} {
			if secret == "" {
				continue
			}
			if strings.HasPrefix(secret, SecretCipherPrefix) {
				hasEncrypted = true
			} else {
				needsMigration = true
			}
		}
	}

	var key []byte
	if hasEncrypted {
		var err error
		key, err = loadMasterKey(st, false)
		if err != nil {
			return Config{}, false, err
		}
	}

	for i := range out.Providers {
		if raw := strings.TrimSpace(out.Providers[i].APIKey); raw != "" {
			if strings.HasPrefix(raw, SecretCipherPrefix) {
				plain, _, err := decryptSecretWithKey(key, raw)
				if err != nil {
					return Config{}, false, fmt.Errorf("decrypt provider %s api_key: %w", out.Providers[i].ID, err)
				}
				out.Providers[i].APIKey = plain
			} else {
				out.Providers[i].APIKey = raw
			}
		}
		if raw := strings.TrimSpace(out.Providers[i].ManagementKey); raw != "" {
			if strings.HasPrefix(raw, SecretCipherPrefix) {
				plain, _, err := decryptSecretWithKey(key, raw)
				if err != nil {
					return Config{}, false, fmt.Errorf("decrypt provider %s management_key: %w", out.Providers[i].ID, err)
				}
				out.Providers[i].ManagementKey = plain
			} else {
				out.Providers[i].ManagementKey = raw
			}
		}
	}
	return normalizeConfig(out), needsMigration, nil
}
