package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestUserInputAvailableInOrdinaryCodexStartup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured []string
		want       []string
	}{
		{"default", nil, []string{"app-server", "--listen", "stdio://", "--enable", "default_mode_request_user_input"}},
		{"configured app server", []string{"app-server", "--stdio", "-c", "model=fixture"}, []string{"app-server", "--stdio", "-c", "model=fixture", "--enable", "default_mode_request_user_input"}},
		{"custom helper", []string{"-test.run=Fixture"}, []string{"-test.run=Fixture"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeSSHSession()
			defer fake.Close()
			go func() {
				decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
				for {
					var request map[string]any
					if decoder.Decode(&request) != nil {
						return
					}
					if request["method"] == "initialized" {
						continue
					}
					if encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{}}) != nil {
						return
					}
				}
			}()
			runtime := New(Config{Executable: "codex", Arguments: tc.configured})
			defer runtime.Close()
			var got []string
			runtime.sshStarter = func(_ context.Context, spec ssh.CommandSpec) (SSHSession, error) {
				got = append([]string(nil), spec.Args...)
				return fake, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := runtime.start(ctx, "/work", nil); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("startup arguments = %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(runtime.config.Arguments, tc.configured) {
				t.Fatal("startup mutated configured arguments")
			}
		})
	}
}
