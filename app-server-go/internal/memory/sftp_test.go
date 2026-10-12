package memory

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var errSFTPNotExist = errors.New("sftp fixture missing")

type sftpProviderFixture struct {
	client SFTPClient
	err    error
	target Target
}

func (p *sftpProviderFixture) Client(_ context.Context, target Target) (SFTPClient, error) {
	p.target = target
	return p.client, p.err
}

type sftpClientFixture struct {
	files       map[string][]byte
	directories []string
	writes      []string
	renames     [][2]string
	removes     []string
	failRename  bool
}

func (c *sftpClientFixture) ReadFile(_ context.Context, filename string) ([]byte, error) {
	raw, ok := c.files[filename]
	if !ok {
		return nil, errSFTPNotExist
	}
	return append([]byte(nil), raw...), nil
}

func (c *sftpClientFixture) WriteFile(_ context.Context, filename string, raw []byte, _ uint32) error {
	c.writes = append(c.writes, filename)
	c.files[filename] = append([]byte(nil), raw...)
	return nil
}

func (c *sftpClientFixture) Rename(_ context.Context, oldName, newName string) error {
	c.renames = append(c.renames, [2]string{oldName, newName})
	if c.failRename {
		return errors.New("rename refused")
	}
	c.files[newName] = c.files[oldName]
	delete(c.files, oldName)
	return nil
}

func (c *sftpClientFixture) Remove(_ context.Context, filename string) error {
	c.removes = append(c.removes, filename)
	delete(c.files, filename)
	return nil
}

func (c *sftpClientFixture) MkdirAll(_ context.Context, filename string, _ uint32) error {
	c.directories = append(c.directories, filename)
	return nil
}

func (*sftpClientFixture) IsNotExist(err error) bool { return errors.Is(err, errSFTPNotExist) }

func TestSFTPTargetFSUsesInjectedProviderAndAtomicRename(t *testing.T) {
	t.Parallel()
	client := &sftpClientFixture{files: map[string][]byte{}}
	provider := &sftpProviderFixture{client: client}
	filesystem := SFTPTargetFS{Provider: provider}
	target := Target{ID: "ssh:42", Kind: TargetSSH, ConnectionID: "42"}
	filename := "/home/fixture/.openviking/ovcli.conf"
	raw := []byte(`{"url":"https://ov.test"}`)
	if err := filesystem.WriteFileAtomic(context.Background(), target, filename, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := filesystem.ReadFile(context.Background(), target, filename)
	if err != nil || !reflect.DeepEqual(actual, raw) {
		t.Fatalf("actual=%q err=%v", actual, err)
	}
	temporary := filename + ".astrorder-memory.tmp"
	if provider.target.ConnectionID != "42" || !reflect.DeepEqual(client.directories, []string{"/home/fixture/.openviking"}) || !reflect.DeepEqual(client.writes, []string{temporary}) || !reflect.DeepEqual(client.renames, [][2]string{{temporary, filename}}) {
		t.Fatalf("target=%#v dirs=%#v writes=%#v renames=%#v", provider.target, client.directories, client.writes, client.renames)
	}
}

func TestSFTPTargetFSCleansTemporaryOnRenameFailure(t *testing.T) {
	t.Parallel()
	client := &sftpClientFixture{files: map[string][]byte{}, failRename: true}
	filesystem := SFTPTargetFS{Provider: &sftpProviderFixture{client: client}}
	filename := "/home/fixture/config.json"
	err := filesystem.WriteFileAtomic(context.Background(), Target{Kind: TargetSSH}, filename, []byte("fixture"), 0o600)
	if err == nil || !strings.Contains(err.Error(), "atomic SFTP rename") || !reflect.DeepEqual(client.removes, []string{filename + ".astrorder-memory.tmp"}) {
		t.Fatalf("err=%v removes=%#v", err, client.removes)
	}
	if _, err := filesystem.ReadFile(context.Background(), Target{Kind: TargetSSH}, "/missing"); !errors.Is(err, ErrTargetFileNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}
