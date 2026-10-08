package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestConnectorEndpointRejectsUnauthenticated(t *testing.T) {
	d := New("secret")
	d.SetConnectorSecret("connector-secret-123")
	server := httptest.NewServer(d.Handler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/v1/connector"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Missing header
	_, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected dial to fail or reject, got resp: %v", resp)
	}

	// Wrong secret
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer wrong-secret"},
		},
	}
	_, resp, err = websocket.Dial(ctx, wsURL, opts)
	if err == nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected wrong secret to fail, got resp: %v", resp)
	}
}

func TestConnectorHelloAndPing(t *testing.T) {
	d := New("secret")
	d.SetConnectorSecret("connector-secret-123")
	server := httptest.NewServer(d.Handler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/v1/connector"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer connector-secret-123"},
		},
	}
	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("failed to dial connector websocket: %v", err)
	}
	defer conn.CloseNow()

	// Send Hello frame
	hello := map[string]any{
		"type":             "hello",
		"protocol_version": 1,
		"agent": map[string]any{
			"id":   "remote-custom-agent",
			"name": "Custom Remote",
		},
	}
	data, _ := json.Marshal(hello)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("failed to write hello: %v", err)
	}

	// Wait briefly for daemon to process hello frame
	var connectors []any
	for i := 0; i < 20; i++ {
		st := d.status("req-1")
		if cs, ok := st["connectors"].([]any); ok && len(cs) > 0 {
			connectors = cs
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(connectors) == 0 {
		t.Fatalf("expected registered connector in status")
	}

	// Send Ping
	ping := map[string]any{"type": "ping"}
	pdata, _ := json.Marshal(ping)
	if err := conn.Write(ctx, websocket.MessageText, pdata); err != nil {
		t.Fatalf("failed to write ping: %v", err)
	}

	// Read Pong
	_, respBytes, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read pong: %v", err)
	}
	var pong map[string]any
	if err := json.Unmarshal(respBytes, &pong); err != nil || pong["type"] != "pong" {
		t.Fatalf("expected pong response, got: %s", string(respBytes))
	}
}

func TestConnectorEventBuffersAndRoutesToBoundControlSessionAndDirectSession(t *testing.T) {
	d := New("secret")
	d.SetConnectorSecret("connector-secret-abc")
	server := httptest.NewServer(d.Handler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/v1/connector"

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer connector-secret-abc"},
		},
	}
	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.CloseNow()

	// 1. Hello
	agent := map[string]any{
		"id":   "daemon-hermes-agent",
		"kind": "hermes",
		"name": "Daemon Hermes",
	}
	hello := map[string]any{"type": "hello", "protocol_version": 1.0, "agent": agent}
	data, _ := json.Marshal(hello)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}

	// Wait briefly for hello to register
	for i := 0; i < 20; i++ {
		st := d.status("req-st")
		if cs, ok := st["connectors"].([]any); ok && len(cs) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 2. Send agent.upsert event before any control session is bound
	ev1 := map[string]any{
		"type": "event",
		"event": map[string]any{
			"id":       "agent-ev-1",
			"type":     "agent.upsert",
			"agent_id": "daemon-hermes-agent",
			"data":     agent,
		},
	}
	ev1Bytes, _ := json.Marshal(ev1)
	if err := conn.Write(ctx, websocket.MessageText, ev1Bytes); err != nil {
		t.Fatal(err)
	}

	// Allow daemon goroutine to process frame
	time.Sleep(50 * time.Millisecond)

	// 3. Bind control session for this agent
	d.BindAgentControlSession("control-sess-1", map[string]any{"agent_id": "daemon-hermes-agent"})

	time.Sleep(50 * time.Millisecond)

	// Verify control session received buffered events
	syncReq := map[string]any{
		"action":     "session.sync",
		"request_id": "sync-1",
		"sessions":   map[string]any{"control-sess-1": 0},
	}
	syncResp := d.sync(syncReq)
	sessionsMap, ok := syncResp["sessions"].(map[string]any)
	if !ok {
		t.Fatalf("expected sessions in syncResp, got: %+v", syncResp)
	}
	controlSess, ok := sessionsMap["control-sess-1"].(map[string]any)
	if !ok {
		t.Fatalf("expected control-sess-1 in sessionsMap, got: %+v", sessionsMap)
	}
	frames, ok := controlSess["frames"].([]map[string]any)
	if !ok || len(frames) == 0 {
		t.Fatalf("expected frames in control-sess-1, got: %+v", controlSess)
	}
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 buffered frames (hello and agent.upsert), got: %d", len(frames))
	}
	if frames[0]["event"] != "connector.hello" {
		t.Fatalf("expected first frame to be connector.hello, got %v", frames[0]["event"])
	}
	if frames[1]["event"] != "connector.event" {
		t.Fatalf("expected second frame to be connector.event, got %v", frames[1]["event"])
	}

	// 4. Send event targeted directly at a native session
	ev2 := map[string]any{
		"type": "event",
		"event": map[string]any{
			"id":         "sess-ev-2",
			"type":       "session.upsert",
			"agent_id":   "daemon-hermes-agent",
			"session_id": "native-session-99",
			"data": map[string]any{
				"id":     "native-session-99",
				"status": "running",
			},
		},
	}
	ev2Bytes, _ := json.Marshal(ev2)
	if err := conn.Write(ctx, websocket.MessageText, ev2Bytes); err != nil {
		t.Fatal(err)
	}

	// Wait and verify native session received event in journal
	var nativeFrames []map[string]any
	for i := 0; i < 20; i++ {
		res := d.sync(map[string]any{"action": "session.sync", "request_id": "sync-2", "sessions": map[string]any{"native-session-99": 0}})
		if sMap, ok := res["sessions"].(map[string]any); ok {
			if sObj, ok := sMap["native-session-99"].(map[string]any); ok {
				if fList, ok := sObj["frames"].([]map[string]any); ok && len(fList) > 0 {
					nativeFrames = fList
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(nativeFrames) == 0 {
		t.Fatalf("expected native session frames for native-session-99")
	}
	if nativeFrames[0]["event"] != "connector.event" {
		t.Fatalf("expected connector.event, got %v", nativeFrames[0]["event"])
	}
}
