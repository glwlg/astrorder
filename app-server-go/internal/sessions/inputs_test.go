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
