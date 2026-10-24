package runtime

import (
	"context"
	"errors"
	"testing"
)

type closeAdmissionRuntime struct{ entered, proceed chan struct{} }

func (a *closeAdmissionRuntime) Create(context.Context, Request) (Result, error) {
	return Result{SessionID: "s", Status: "idle"}, nil
}
func (a *closeAdmissionRuntime) Command(_ context.Context, r Request) (Result, error) {
	close(a.entered)
	<-a.proceed
	return Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"closed": r.Action == "session.close"}}, nil
}
func TestCloseAdmissionExcludesConcurrentOperations(t *testing.T) {
	for _, first := range []string{"session.send", "session.close"} {
		t.Run(first, func(t *testing.T) {
			ctx := context.Background()
			r := NewRegistry()
			a := &closeAdmissionRuntime{make(chan struct{}), make(chan struct{})}
			r.Register("test", a)
			if _, err := r.Create(ctx, Request{AgentType: "test"}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := r.Command(ctx, Request{SessionID: "s", Action: first}); done <- err }()
			<-a.entered
			second := "session.close"
			if first == second {
				second = "session.send"
			}
			_, err := r.Command(ctx, Request{SessionID: "s", Action: second})
			close(a.proceed)
			if !errors.Is(err, ErrRuntimeBusy) {
				t.Fatalf("concurrent %s: %v", second, err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if r.inflight != 0 || r.operations["test"] != 0 || r.reloading["test"] {
				t.Fatal("admission leaked")
			}
		})
	}
}
