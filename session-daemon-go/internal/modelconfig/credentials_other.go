//go:build !windows

package modelconfig

import (
	"context"
	"errors"
	"os"
	"sync"
)

// Unix credentials are staged into the selected home's environment.d file and
// published into this daemon's environment for subsequently launched natives.
type fileCredentialStore struct{}

func NativeCredentialStore() CredentialStore      { return fileCredentialStore{} }
func (fileCredentialStore) EnvironmentFile() bool { return true }
func (fileCredentialStore) Set(ctx context.Context, key string) (func() error, error) {
	return fileCredentialStore{}.SetMany(ctx, map[string]string{EnvTokenKey: key})
}

func (fileCredentialStore) SetMany(ctx context.Context, values map[string]string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type priorEnv struct {
		name, value string
		existed     bool
	}
	priors := make([]priorEnv, 0, len(values))
	for _, name := range credentialNames {
		value := values[name]
		if value == "" {
			continue
		}
		if err := validateAPIKey(value); err != nil {
			return nil, err
		}
		prior, existed := os.LookupEnv(name)
		priors = append(priors, priorEnv{name: name, value: prior, existed: existed})
	}
	var once sync.Once
	var restoreErr error
	undo := func() error {
		once.Do(func() {
			for i := len(priors) - 1; i >= 0; i-- {
				item := priors[i]
				if item.existed {
					restoreErr = errors.Join(restoreErr, os.Setenv(item.name, item.value))
				} else {
					restoreErr = errors.Join(restoreErr, os.Unsetenv(item.name))
				}
			}
		})
		return restoreErr
	}
	for _, name := range credentialNames {
		value := values[name]
		if value == "" {
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			return undo, err
		}
	}
	return undo, nil
}
