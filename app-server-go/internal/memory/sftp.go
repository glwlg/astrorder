package memory

import (
	"context"
	"errors"
	"fmt"
	"path"
)

// SFTPClient is the minimal capability expected from a host-key-verified SSH adapter.
// Authentication, host-key policy, and connection lifetime remain with the injected
// shared SSH transport; this package never creates an insecure SSH client.
type SFTPClient interface {
	ReadFile(context.Context, string) ([]byte, error)
	WriteFile(context.Context, string, []byte, uint32) error
	Rename(context.Context, string, string) error
	Remove(context.Context, string) error
	MkdirAll(context.Context, string, uint32) error
	IsNotExist(error) bool
}

type SFTPProvider interface {
	Client(context.Context, Target) (SFTPClient, error)
}

type SFTPTargetFS struct {
	Provider SFTPProvider
}

func (f SFTPTargetFS) ReadFile(ctx context.Context, target Target, filename string) ([]byte, error) {
	client, err := f.client(ctx, target)
	if err != nil {
		return nil, err
	}
	raw, err := client.ReadFile(ctx, filename)
	if err != nil && client.IsNotExist(err) {
		return nil, ErrTargetFileNotFound
	}
	return raw, err
}

func (f SFTPTargetFS) WriteFileAtomic(ctx context.Context, target Target, filename string, raw []byte, mode uint32) error {
	client, err := f.client(ctx, target)
	if err != nil {
		return err
	}
	if err := client.MkdirAll(ctx, path.Dir(filename), 0o700); err != nil {
		return err
	}
	temporary := filename + ".astrorder-memory.tmp"
	if err := client.WriteFile(ctx, temporary, raw, mode); err != nil {
		return err
	}
	if err := client.Rename(ctx, temporary, filename); err != nil {
		_ = client.Remove(ctx, temporary)
		return fmt.Errorf("atomic SFTP rename: %w", err)
	}
	return nil
}

func (f SFTPTargetFS) client(ctx context.Context, target Target) (SFTPClient, error) {
	if target.Kind != TargetSSH {
		return nil, fmt.Errorf("SFTP filesystem cannot serve %s target", target.Kind)
	}
	if f.Provider == nil {
		return nil, errors.New("host-key-verified SFTP provider is required")
	}
	client, err := f.Provider.Client(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("open host-key-verified SFTP client: %w", err)
	}
	if client == nil {
		return nil, errors.New("SFTP provider returned no client")
	}
	return client, nil
}
