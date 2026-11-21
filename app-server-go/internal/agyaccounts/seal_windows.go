package agyaccounts

import "astrorder.dev/app-server/internal/credentials"

func (*Vault) seal(data []byte, _ bool) ([]byte, error) { return credentials.Encrypt(data) }
func (*Vault) unseal(data []byte) ([]byte, error)       { return credentials.Decrypt(data) }
