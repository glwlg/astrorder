//go:build windows

package modelconfig

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows/registry"
	"os"
	"sync"
)

type windowsCredentialStore struct{ path, name string }

func NativeCredentialStore() CredentialStore {
	return &windowsCredentialStore{path: `Environment`, name: EnvTokenKey}
}
func (s *windowsCredentialStore) Set(ctx context.Context, value string) (func() error, error) {
	return s.setNamed(ctx, s.name, value)
}
func (s *windowsCredentialStore) SetMany(ctx context.Context, values map[string]string) (func() error, error) {
	var restores []func() error
	restore := func() error {
		var err error
		for i := len(restores) - 1; i >= 0; i-- {
			err = errors.Join(err, restores[i]())
		}
		return err
	}
	for _, envName := range credentialNames {
		value := values[envName]
		if value == "" {
			continue
		}
		undo, err := s.setNamed(ctx, envName, value)
		if undo != nil {
			restores = append(restores, undo)
		}
		if err != nil {
			return restore, err
		}
	}
	return restore, nil
}
func (s *windowsCredentialStore) setNamed(ctx context.Context, name, value string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateAPIKey(value); err != nil {
		return nil, err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.path, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil, fmt.Errorf("open native credential store: %w", err)
	}
	defer key.Close()
	old, kind, err := key.GetStringValue(name)
	existed := err == nil
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return nil, fmt.Errorf("read native credential state: %w", err)
	}
	priorEnv, hadEnv := os.LookupEnv(name)
	var once sync.Once
	var restoreErr error
	restore := func() error {
		once.Do(func() {
			restoreKey, err := registry.OpenKey(registry.CURRENT_USER, s.path, registry.QUERY_VALUE|registry.SET_VALUE)
			if err != nil {
				restoreErr = err
				return
			}
			defer restoreKey.Close()
			if existed {
				if kind == registry.EXPAND_SZ {
					restoreErr = restoreKey.SetExpandStringValue(name, old)
				} else {
					restoreErr = restoreKey.SetStringValue(name, old)
				}
			} else {
				restoreErr = restoreKey.DeleteValue(name)
				if errors.Is(restoreErr, registry.ErrNotExist) {
					restoreErr = nil
				}
			}
			if hadEnv {
				restoreErr = errors.Join(restoreErr, os.Setenv(name, priorEnv))
			} else {
				restoreErr = errors.Join(restoreErr, os.Unsetenv(name))
			}
		})
		return restoreErr
	}
	if err := key.SetStringValue(name, value); err != nil {
		return restore, fmt.Errorf("write native credential store: %w", err)
	}
	readback, _, err := key.GetStringValue(name)
	if err != nil || readback != value {
		return restore, fmt.Errorf("native credential readback did not match")
	}
	if err := ctx.Err(); err != nil {
		return restore, err
	}
	if err := os.Setenv(name, value); err != nil {
		return restore, fmt.Errorf("publish native credential environment: %w", err)
	}
	return restore, nil
}
