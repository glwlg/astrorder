package remote

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/activity"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
)

type fakeTail struct {
	reader io.ReadCloser
	closed atomic.Bool
}

func (f *fakeTail) Stdout() io.ReadCloser              { return f.reader }
func (f *fakeTail) Wait() (sshtrans.ExitStatus, error) { return sshtrans.ExitStatus{}, nil }
func (f *fakeTail) Close() error                       { f.closed.Store(true); return f.reader.Close() }

func eventLine(id string, session string) string {
	return fmt.Sprintf(`{"id":%q,"session_id":%q,"event":"UserPromptSubmit","observed_at":%d}`+"\n", id, session, time.Now().Unix())
}

func TestSuperviseTailReconnectsAndForwardsActivity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	start := func(context.Context, sshtrans.CommandSpec) (TailSession, error) {
		n := attempts.Add(1)
		if n == 1 {
			return nil, fmt.Errorf("connection refused")
		}
		id := strings.Repeat(fmt.Sprintf("%x", n), 32)[:32]
		return &fakeTail{reader: io.NopCloser(strings.NewReader(eventLine(id, fmt.Sprintf("s%d", n))))}, nil
	}
	var mu sync.Mutex
	got := map[string]string{}
	opts := activity.Options{
		AgentID: "ssh-codex-x",
		Known:   func(string) bool { return false },
		Emit: func(session, event string, payload map[string]any, status string) error {
			mu.Lock()
			got[session] = event + "/" + status
			mu.Unlock()
			if len(got) == 2 {
				cancel()
			}
			return nil
		},
	}
	var slept []time.Duration
	sleep := func(_ context.Context, d time.Duration) { slept = append(slept, d) }
	done := make(chan struct{})
	go func() { SuperviseTail(ctx, start, opts, sleep); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop after cancellation")
	}
	mu.Lock()
	defer mu.Unlock()
	if got["s2"] != "native.activity/idle" || got["s3"] != "native.activity/idle" {
		t.Fatalf("events after a failed start and a clean EOF were not forwarded: %v", got)
	}
	if len(slept) < 2 || slept[0] != tailBackoffMin || slept[1] != 2*tailBackoffMin {
		t.Fatalf("backoff must double on repeated short-lived streams: %v", slept)
	}
}

func TestSuperviseTailStopsPromptlyWhileBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pipeReader, pipeWriter := io.Pipe()
	defer pipeWriter.Close()
	session := &fakeTail{reader: pipeReader}
	start := func(context.Context, sshtrans.CommandSpec) (TailSession, error) { return session, nil }
	done := make(chan struct{})
	go func() { SuperviseTail(ctx, start, activity.Options{}, nil); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a stream blocked on a silent host must be torn down on cancel")
	}
	if !session.closed.Load() {
		t.Fatal("session was not closed")
	}
}

func TestTailScriptFailsLoudlyWithoutHelper(t *testing.T) {
	for _, want := range []string{"exit 127", "tail --spool", "astrorder/helper/*/astrorder-helper"} {
		if !strings.Contains(tailScript, want) {
			t.Fatalf("tail script lost %q", want)
		}
	}
}
