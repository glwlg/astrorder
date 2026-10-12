package memory

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type wslExecutorFixture struct {
	executable string
	args       []string
	stdin      []byte
	result     WSLCommandResult
	err        error
}

func (f *wslExecutorFixture) Execute(_ context.Context, executable string, args []string, stdin []byte) (WSLCommandResult, error) {
	f.executable = executable
	f.args = append([]string(nil), args...)
	f.stdin = append([]byte(nil), stdin...)
	return f.result, f.err
}

func TestWSLTransportUsesExactNativeArgvAndFramedStdin(t *testing.T) {
	t.Parallel()
	frame := []byte{0, 0, 0, 1, '{'}
	executor := &wslExecutorFixture{result: WSLCommandResult{Stdout: []byte("framed-response")}}
	transport := WSLTransport{Executable: "wsl-fixture.exe", HelperPath: "/usr/local/libexec/astrorder-target-fs", Executor: executor}
	actual, err := transport.Exchange(context.Background(), Target{ID: "wsl:Ubuntu-24.04", Kind: TargetWSL, Distro: "Ubuntu-24.04"}, frame)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"-d", "Ubuntu-24.04", "--exec", "/usr/local/libexec/astrorder-target-fs", "target-fs-v1"}
	if executor.executable != "wsl-fixture.exe" || !reflect.DeepEqual(executor.args, wantArgs) || !reflect.DeepEqual(executor.stdin, frame) || string(actual) != "framed-response" {
		t.Fatalf("executable=%q args=%#v stdin=%v actual=%q", executor.executable, executor.args, executor.stdin, actual)
	}
}

func TestWSLTransportRejectsUnsafeDistroAndReportsHelperFailure(t *testing.T) {
	t.Parallel()
	transport := WSLTransport{HelperPath: "/helper", Executor: &wslExecutorFixture{}}
	if _, err := transport.Exchange(context.Background(), Target{Kind: TargetWSL, Distro: "Ubuntu; touch bad"}, nil); err == nil {
		t.Fatal("accepted unsafe distro")
	}
	executor := &wslExecutorFixture{result: WSLCommandResult{ExitCode: 7, Stderr: []byte("helper refused")}, err: errors.New("exit status 7")}
	transport.Executor = executor
	_, err := transport.Exchange(context.Background(), Target{Kind: TargetWSL, Distro: "Ubuntu"}, nil)
	if err == nil || !strings.Contains(err.Error(), "helper refused") {
		t.Fatalf("err = %v", err)
	}
}
