package hermes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCanonicalTransportUsesExistingOwner(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"hermes-gateway-v1"}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		defer close(closed)
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(data, &req)
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":"req","result":{"ok":true}}`))
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	transport, err := dialCanonical(ctx, "ws"+server.URL[4:], []string{"hermes-gateway-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transport.Write([]byte(`{"jsonrpc":"2.0","id":"req","method":"ping"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1024)
	n, err := transport.Read(buffer)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if n == 0 || buffer[n-1] != '\n' {
		t.Fatalf("RPC frame missing line boundary: %q", buffer[:n])
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("viewer did not detach")
	}
}

func TestCanonicalCreateDoesNotResolveDisplayTitle(t *testing.T) {
	a := New(Config{})
	_, params, err := a.canonicalRequest(context.Background(), "session.create", map[string]any{"source": "local", "title": "新会话", "reasoning_effort": "max"})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := params["title"]; exists {
		t.Fatal("display title would resolve an existing conversation")
	}
	if params["reasoning"] != "max" {
		t.Fatal("canonical reasoning missing")
	}
}

func TestCanonicalParamsAndSnapshot(t *testing.T) {
	a := New(Config{})
	a.canonical = true
	method, params, err := a.canonicalRequest(context.Background(), "session.resume", map[string]any{"session_id": "chat", "lazy": true, "omit_messages": true})
	if err != nil || method != "session.resume" {
		t.Fatalf("%s %v", method, err)
	}
	if _, exists := params["lazy"]; exists {
		t.Fatal("legacy lazy key sent to strict canonical owner")
	}
	_, params, err = a.canonicalRequest(context.Background(), "prompt.submit", map[string]any{"session_id": "chat", "text": "hello"})
	if err != nil || params["submission_id"] == nil || params["submission_id"] == "" {
		t.Fatalf("missing admission identity: %#v %v", params, err)
	}
	result := canonicalResult("session.resume", map[string]any{"session_id": "chat", "revision": json.Number("4"), "info": map[string]any{"model": "test"}})
	if result["id"] != "chat" {
		t.Fatalf("snapshot handle not normalized: %#v", result)
	}
}
