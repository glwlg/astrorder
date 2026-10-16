package grok

import (
	"context"
	"io"
	"reflect"
	"testing"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

type deleteSSHFixture struct {
	status   ssh.ExitStatus
	closeErr error
}

func (*deleteSSHFixture) Stdin() io.WriteCloser           { return nil }
func (*deleteSSHFixture) Stdout() io.ReadCloser           { return nil }
func (*deleteSSHFixture) Stderr() []byte                  { return nil }
func (f *deleteSSHFixture) Wait() (ssh.ExitStatus, error) { return f.status, nil }
func (f *deleteSSHFixture) Close() error                  { return f.closeErr }

func TestGrokRemoteDeleteRequiresConfirmedExit(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[confirmed], func(t *testing.T) {
			var spec ssh.CommandSpec
			a := NewWithSSHStarter(Config{Executable: "/opt/bin/grok", Workspace: "/scratch"}, func(_ context.Context, s ssh.CommandSpec) (SSHSession, error) {
				spec = s
				return &deleteSSHFixture{status: ssh.ExitStatus{ExitCode: 0, ConfirmedRemoteExit: confirmed}}, nil
			})
			_, err := a.Command(context.Background(), core.Request{SessionID: "native;literal", Action: "session.delete"})
			if (err == nil) != confirmed {
				t.Fatalf("confirmed=%v error=%v", confirmed, err)
			}
			if spec.Executable != "/opt/bin/grok" || !reflect.DeepEqual(spec.Args, []string{"sessions", "delete", "native;literal"}) {
				t.Fatalf("unsafe argv: %+v", spec)
			}
		})
	}
}
