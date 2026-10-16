package sessions

import (
	"astrorder.dev/app-server/internal/targetfs"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisteredAttachmentsUseBytesAndScopedTargetFiles(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	h.attachmentsDir = t.TempDir()
	work := t.TempDir()
	h.resolver = targetfs.NewResolver(targetfs.ResolverOptions{Store: st, DefaultWorkspace: work, AllowedRoots: []string{work}})
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.attachmentsDir, "record.blob"), []byte("file bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttachment(ctx, map[string]any{"id": "att", "name": "note.txt", "media_type": "text/plain", "storage_name": "record.blob", "size": 10}); err != nil {
		t.Fatal(err)
	}
	cmd := map[string]any{"id": "command", "agent_id": "a", "text": "request", "attachments": []any{map[string]any{"id": "att"}}}
	session := map[string]any{"workspace": work}
	parts, err := h.commandInputs(ctx, cmd, session)
	if err != nil {
		t.Fatal(err)
	}
	text := parts[0].GetText().Text
	if !strings.Contains(text, ".astrorder/attachments/") || strings.Contains(text, h.attachmentsDir) {
		t.Fatalf("attachment input target=%s", text)
	}
	matches, err := filepath.Glob(filepath.Join(work, ".astrorder", "attachments", "*", "note.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("staged files=%v %v", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil || string(data) != "file bytes" {
		t.Fatal("staged attachment mismatch")
	}
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	parts, err = h.commandInputs(ctx, cmd, session)
	if err != nil || len(parts) != 2 || string(parts[1].GetAttachment().Content) != "file bytes" {
		t.Fatalf("path-free Hermes bytes=%+v %v", parts, err)
	}
	h.maxAttachmentSize = 3
	if _, err = h.commandInputs(ctx, cmd, session); err == nil {
		t.Fatal("oversize attachment accepted")
	}
}
func TestQueuedSendUsesClaimedPayloadAndDoesNotDispatchTwice(t *testing.T) {
	commandSendCalls = 0
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "fixture", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Submit(ctx, map[string]any{"id": "queued", "agent_id": "a", "session_id": "s", "action": "enqueue", "text": "stored text"}); err != nil {
		t.Fatal(err)
	}
	for i, expected := range []int{200, 409} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/commands/queued/send", strings.NewReader(`{"agent_id":"a","session_id":"s"}`))
		r.Header.Set("Authorization", "Bearer browser-sec")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatalf("send %d=%d %s", i, w.Code, w.Body.String())
		}
	}
	if commandSendCalls != 1 {
		t.Fatalf("dispatch count=%d", commandSendCalls)
	}
}


func TestGrokAttachmentValidationAndDelivery(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	h.attachmentsDir = t.TempDir()
	work := t.TempDir()

	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "grok-agent", "kind": "grok", "name": "Grok Agent", "status": "ready"}); err != nil {
		t.Fatal(err)
	}

	// 1. Text attachment should succeed and deliver content bytes
	if err := os.WriteFile(filepath.Join(h.attachmentsDir, "text.blob"), []byte("hello grok text"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttachment(ctx, map[string]any{"id": "att-text", "name": "test.txt", "media_type": "text/plain", "storage_name": "text.blob", "size": 15}); err != nil {
		t.Fatal(err)
	}
	cmdText := map[string]any{"id": "cmd-text", "agent_id": "grok-agent", "text": "review this", "attachments": []any{map[string]any{"id": "att-text"}}}
	parts, err := h.commandInputs(ctx, cmdText, map[string]any{"workspace": work})
	if err != nil {
		t.Fatalf("text attachment should succeed, got: %v", err)
	}
	if len(parts) != 2 || parts[1].GetAttachment() == nil {
		t.Fatalf("expected 2 parts with attachment, got: %+v", parts)
	}
	att := parts[1].GetAttachment()
	if att.Name != "test.txt" || string(att.Content) != "hello grok text" {
		t.Fatalf("attachment content mismatch: %+v", att)
	}

	// 2. Image attachment should be explicitly rejected (image=false)
	if err := os.WriteFile(filepath.Join(h.attachmentsDir, "image.blob"), []byte("fake png bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttachment(ctx, map[string]any{"id": "att-img", "name": "pic.png", "media_type": "image/png", "storage_name": "image.blob", "size": 14}); err != nil {
		t.Fatal(err)
	}
	cmdImg := map[string]any{"id": "cmd-img", "agent_id": "grok-agent", "text": "look at image", "attachments": []any{map[string]any{"id": "att-img"}}}
	if _, err := h.commandInputs(ctx, cmdImg, map[string]any{"workspace": work}); err == nil {
		t.Fatal("expected error rejecting image attachment for grok, got nil")
	}

	// 3. Binary attachment should be explicitly rejected
	if err := os.WriteFile(filepath.Join(h.attachmentsDir, "bin.blob"), []byte{0xff, 0xfe, 0x00, 0x01, 0x80}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttachment(ctx, map[string]any{"id": "att-bin", "name": "data.bin", "media_type": "application/octet-stream", "storage_name": "bin.blob", "size": 5}); err != nil {
		t.Fatal(err)
	}
	cmdBin := map[string]any{"id": "cmd-bin", "agent_id": "grok-agent", "text": "run binary", "attachments": []any{map[string]any{"id": "att-bin"}}}
	if _, err := h.commandInputs(ctx, cmdBin, map[string]any{"workspace": work}); err == nil {
		t.Fatal("expected error rejecting binary attachment for grok, got nil")
	}
}