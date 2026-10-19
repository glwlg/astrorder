package hermes

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestCanonicalResumePreservesNativeActivity(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "waiting"}[waiting], func(t *testing.T) {
			root := t.TempDir()
			a := New(Config{Workspace: root})
			a.canonical = true
			s := &SessionState{SessionID: "s", Status: "idle", Metadata: map[string]any{}}
			a.sessions["s"] = s
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			a.stdin = writer
			a.processDone = make(chan struct{})
			done := make(chan struct{})
			defer func() { writer.Close(); reader.Close(); <-done }()
			go func() {
				defer close(done)
				d := json.NewDecoder(reader)
				for {
					var req map[string]any
					if d.Decode(&req) != nil {
						return
					}
					result := map[string]any{}
					if req["method"] == "session.resume" {
						prompts := []any{}
						if waiting {
							prompts = append(prompts, map[string]any{"kind": "approval", "prompt_id": "p"})
						}
						result = map[string]any{"session_id": "s", "running": true, "prompts": prompts, "info": map[string]any{"cwd": root}}
					}
					a.mu.RLock()
					pending := a.pending[req["id"].(string)]
					a.mu.RUnlock()
					pending.ch <- &rpcMessage{Result: result}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := a.ensureHandle(ctx, s); err != nil {
				t.Fatal(err)
			}
			want := "running"
			if waiting {
				want = "waiting_approval"
			}
			if s.Status != want {
				t.Fatalf("resumed activity=%s want=%s", s.Status, want)
			}
		})
	}
}
