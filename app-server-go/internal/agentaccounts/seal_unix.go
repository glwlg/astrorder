//go:build !windows

package agentaccounts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

const keyBytes = 32

func (v *Vault) aead(create bool) (cipher.AEAD, error) {
	path := filepath.Join(v.dir, ".key")
	key, err := readPrivate(path, keyBytes)
	if errors.Is(err, os.ErrNotExist) && create {
		key = make([]byte, keyBytes)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		err = writePrivate(v.dir, key, func(temp string) error { return os.Link(temp, path) })
		if errors.Is(err, os.ErrExist) {
			key, err = readPrivate(path, keyBytes)
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != keyBytes {
		return nil, errors.New("invalid account vault key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (v *Vault) seal(data []byte, create bool) ([]byte, error) {
	box, err := v.aead(create)
	if err != nil {
		return nil, err
	}
	return box.Seal(nil, nil, data, nil), nil
}

func (v *Vault) unseal(data []byte) ([]byte, error) {
	box, err := v.aead(false)
	if err != nil {
		return nil, err
	}
	return box.Open(nil, nil, data, nil)
}
