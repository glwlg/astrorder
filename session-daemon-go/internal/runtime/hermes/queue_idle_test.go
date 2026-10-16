package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"errors"
	"testing"
)

func TestAttachmentGuidanceIsDefinitivelyRejectedBeforeRPC(t *testing.T) {
	a := New(Config{})
	s := &SessionState{SessionID: "s", Handle: "h", Status: "running", CommandID: "original", Metadata: map[string]any{}}
	a.sessions["s"] = s
	_, err := a.handleSend(context.Background(), s, core.Request{Fields: map[string]any{"text": "photos", "attachments": []any{map[string]any{"id": "photo", "name": "photo.png", "media_type": "image/png", "content_base64": "eA=="}}}})
	var rejected *core.RejectedBeforeDispatch
	if !errors.As(err, &rejected) {
		t.Fatalf("local rejection became uncertain: %v", err)
	}
	if s.Status != "running" || s.CommandID != "original" || s.sending {
		t.Fatal("rejected guidance changed active turn")
	}
}

func TestQueuedSendNeverSteersAnActiveTurn(t *testing.T) {
	a := New(Config{})
	s := &SessionState{SessionID: "s", Handle: "h", Status: "running", CommandID: "original", Metadata: map[string]any{}}
	a.sessions["s"] = s
	_, err := a.handleSend(context.Background(), s, core.Request{Fields: map[string]any{"text": "queued", "params": map[string]any{"idle_only": true}}})
	if err == nil || err.Error() != "queued send requires an idle session" {
		t.Fatalf("queued send crossed into guidance: %v", err)
	}
	if s.CommandID != "original" || s.sending {
		t.Fatal("queue changed active command")
	}
}
