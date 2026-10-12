package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"github.com/coder/websocket"
)

// Opt-in development chain check uses only a freshly built private daemon.
func TestPrivateDaemonFormalAppStartupAndRestart(t *testing.T) {
	executable := os.Getenv("ASTRORDER_TEST_SESSIOND")
	if executable == "" {
		t.Skip("set ASTRORDER_TEST_SESSIOND to a fresh candidate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	cfgPath := filepath.Join(root, "daemon-config.json")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"runtimes": []any{map[string]any{"type": "pty", "allowed": []string{root}}, map[string]any{"type": "codex", "executable": helper, "arguments": []string{"-test.run=^TestGroupNativeProcessHelper$", "astrorder-group-helper"}, "workspace": root, "allowed": []string{root}, "agent_id": "group-agent"}}})
	if err = os.WriteFile(cfgPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable)
	for _, key := range []string{"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	command.Env = append(command.Env, "USERPROFILE="+home, "HOME="+home,
		"ASTRORDER_SESSION_DAEMON_SECRET=fixture", "ASTRORDER_SESSION_DAEMON_DB="+filepath.Join(root, "daemon.db"),
		"ASTRORDER_SESSION_DAEMON_CONFIG="+cfgPath, "ASTRORDER_SESSION_DAEMON_GRPC_TARGET="+address,
		"ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL=fixture", "ASTRORDER_SESSION_DAEMON_WS_DISABLED=1")
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- command.Wait() }()
	defer func() {
		select {
		case <-processDone:
		default:
			_ = command.Process.Kill()
			<-processDone
		}
	}()
	var client *daemonclient.Client
	for ctx.Err() == nil {
		probeCtx, probeCancel := context.WithTimeout(ctx, 100*time.Millisecond)
		client, err = daemonclient.Dial(probeCtx, daemonclient.Config{Target: address, Credential: "fixture"})
		probeCancel()
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err != nil || client == nil {
		t.Fatalf("private daemon not ready: %v", err)
	}
	defer client.Close()
	epoch := client.DaemonID()
	appConfig := config.Config{Listen: "127.0.0.1:0", DatabasePath: filepath.Join(root, "app.db"), DefaultWorkspace: root, BrowserSecret: "fixture",
		ConnectorSecret: "fixture",
		Daemon:          config.DaemonConfig{Target: address, Credential: "fixture", RequestTimeout: 3 * time.Second}}
	startApp := func() (readyMessage, func()) {
		appCtx, stop := context.WithCancel(ctx)
		reader, writer := io.Pipe()
		result := make(chan error, 1)
		go func() {
			err := runServer(appCtx, appConfig, writer, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_ = writer.CloseWithError(err)
			result <- err
		}()
		var ready readyMessage
		if err = json.NewDecoder(reader).Decode(&ready); err != nil {
			stop()
			t.Fatal(err)
		}
		_ = reader.Close()
		// Keep an unexpected background exit observable before test cleanup.
		go func() {
			select {
			case <-appCtx.Done():
			case err := <-result:
				result <- err
				if err != nil {
					t.Errorf("App exited unexpectedly: %v", err)
				}
			}
		}()
		return ready, func() {
			stop()
			select {
			case err := <-result:
				if err != nil {
					t.Errorf("stop App: %v", err)
				}
			case <-ctx.Done():
				t.Error("App did not stop")
			}
		}
	}
	ready, stopApp := startApp()
	browser, _, err := websocket.Dial(ctx, "ws://"+ready.Address+"/ws/v1/events?token=fixture", nil)
	if err != nil {
		stopApp()
		t.Fatal(err)
	}
	appendEvent := func(eventType string, payload map[string]any) {
		raw, _ := json.Marshal(payload)
		_, err = client.Events.AppendEvent(ctx, &sessiondv1.AppendEventRequest{Session: &sessiondv1.SessionRef{SessionId: "fixture-stream"}, EventType: eventType,
			Status: sessiondv1.SessionStatus_SESSION_STATUS_IDLE, Payload: &sessiondv1.NativeJson{Schema: "astrorder.event.payload.v1", Utf8Json: raw}})
		if err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("connector.hello", map[string]any{"id": "fixture-agent", "kind": "hermes", "name": "Fixture", "status": "connected", "capabilities": []string{}})
	_, raw, err := browser.Read(ctx)
	if err != nil {
		stopApp()
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"type":"agent.upsert"`) {
		stopApp()
		t.Fatalf("unexpected event wire: %s", raw)
	}
	_ = browser.CloseNow()
	stopApp()
	appendEvent("connector.event", map[string]any{"id": "fixture-event", "type": "session.upsert", "agent_id": "fixture-agent", "session_id": "fixture-session", "data": map[string]any{"id": "fixture-session", "title": "Fixture", "workspace": root, "status": "idle"}})
	ready, stopApp = startApp()
	defer stopApp()
	response, err := http.Get("http://" + ready.Address + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("App health failed")
	}
	st, err := store.OpenWithOptions(appConfig.DatabasePath, store.Options{SchemaMode: store.SchemaValidateOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.UpsertAgent(ctx, map[string]any{"id": "group-agent", "kind": "codex", "name": "Group fixture", "status": "connected"}); err != nil {
		t.Fatal(err)
	}
	callGroup := func(path, body string) map[string]any {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+ready.Address+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer fixture")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var payload map[string]any
		if err = json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("group chain %d: %+v", response.StatusCode, payload)
		}
		return payload
	}
	group := callGroup("/api/v1/bot-groups", `{"name":"隔离群聊","members":[{"machine_id":"local","agent_id":"group-agent","name":"测试成员"}]}`)
	groupID := group["group"].(map[string]any)["id"].(string)
	callGroup("/api/v1/bot-groups/"+groupID+"/messages", `{"text":"隔离任务"}`)
	groupWait, groupCancel := context.WithTimeout(ctx, 3*time.Second)
	defer groupCancel()
	for {
		preference, err := st.GetPreference(ctx, "bot_group_msgs:"+groupID)
		if err != nil {
			t.Fatal(err)
		}
		if items, ok := preference.Value.([]any); ok && len(items) > 1 {
			reply := items[len(items)-1].(map[string]any)["text"]
			if reply != "合成原生回复" {
				t.Fatalf("native group reply: %v", reply)
			}
			break
		}
		select {
		case <-groupWait.Done():
			t.Fatal("native group reply not persisted")
		case <-time.After(10 * time.Millisecond):
		}
	}
	checkpoint, err := st.GetCheckpoint(ctx, epoch, "fixture-stream")
	createdSession := callGroup("/api/v1/sessions", `{"agent_id":"group-agent","title":"隔离普通会话"}`)
	nativeSessionID, _ := createdSession["id"].(string)
	if nativeSessionID == "" || createdSession["control_state"] != "owned" {
		t.Fatalf("formal native creation: %+v", createdSession)
	}
	commandBody, _ := json.Marshal(map[string]any{"id": "formal-exact-command", "agent_id": "group-agent", "session_id": nativeSessionID, "action": "send", "text": "隔离普通指令"})
	callGroup("/api/v1/commands", string(commandBody))
	for {
		storedCommand, err := st.GetCommand(ctx, "group-agent", nativeSessionID, "formal-exact-command")
		if err != nil {
			t.Fatal(err)
		}
		if storedCommand != nil && storedCommand["state"] == "completed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("native command completion missing: %+v", storedCommand)
		case <-time.After(10 * time.Millisecond):
		}
	}
	callGroup("/api/v1/commands", string(commandBody))
	activeTurn, err := st.ActiveTurn(ctx, "group-agent", nativeSessionID)
	if err != nil || activeTurn != "" {
		t.Fatalf("completed turn resurrected: %q %v", activeTurn, err)
	}
	historyRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ready.Address+"/api/v1/sessions/"+nativeSessionID+"/messages?agent_id=group-agent", nil)
	historyRequest.Header.Set("Authorization", "Bearer fixture")
	historyResponse, err := http.DefaultClient.Do(historyRequest)
	if err != nil {
		t.Fatal(err)
	}
	historyBytes, err := io.ReadAll(historyResponse.Body)
	historyResponse.Body.Close()
	if err != nil || historyResponse.StatusCode != 200 || !strings.Contains(string(historyBytes), "原生分页记录") {
		t.Fatalf("formal native history: %d %s %v", historyResponse.StatusCode, historyBytes, err)
	}
	project := callGroup("/api/v1/projects", `{"workspace":"`+strings.ReplaceAll(root, "\\", "\\\\")+`","name":"隔离项目","agent_id":"group-agent"}`)["project"].(map[string]any)
	deleteProjectBody, _ := json.Marshal(map[string]any{"project_key": project["id"], "session_keys": []map[string]any{{"agent_id": "group-agent", "id": nativeSessionID}}, "delete_sessions": true})
	deletion := callGroup("/api/v1/projects/delete", string(deleteProjectBody))
	if deletion["ok"] != true || len(deletion["deleted_sessions"].([]any)) != 1 {
		t.Fatalf("formal native project deletion: %+v", deletion)
	}
	deletedRow, err := st.GetSession(ctx, "group-agent", nativeSessionID)
	if err != nil || deletedRow != nil {
		t.Fatalf("native deletion not persisted: %v", err)
	}
	if err != nil || checkpoint != 2 {
		t.Fatalf("checkpoint=%d err=%v", checkpoint, err)
	}
	session, err := st.GetSession(ctx, "fixture-agent", "fixture-session")
	if err != nil || session == nil {
		t.Fatalf("missing offline session projection: %v", err)
	}
	current, err := client.Daemon.GetStatus(ctx, &sessiondv1.GetStatusRequest{})
	if err != nil || current.DaemonId != epoch {
		t.Fatal("App restart changed daemon")
	}
	headers := http.Header{"Authorization": []string{"Bearer fixture"}}
	connector, _, err := websocket.Dial(ctx, "ws://"+ready.Address+"/ws/v1/connector", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer connector.CloseNow()
	sendConnector := func(value any) {
		raw, _ := json.Marshal(value)
		if err := connector.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
	}
	sendConnector(map[string]any{"type": "hello", "protocol_version": 1, "agent": map[string]any{
		"id": "external-agent", "kind": "hermes", "name": "External", "status": "ready", "capabilities": []string{}}})
	sendConnector(map[string]any{"type": "ping"})
	_, pong, err := connector.Read(ctx)
	if err != nil || !strings.Contains(string(pong), `"pong"`) {
		t.Fatalf("connector registration/binding failed: %s %v", pong, err)
	}
	sendConnector(map[string]any{"type": "agent_invoke", "id": "blackboard-request", "capability": "blackboard.set", "input": map[string]any{"namespace": "group:fixture", "key": "connector", "value": "connected"}})
	_, invokeReply, err := connector.Read(ctx)
	if err != nil || !strings.Contains(string(invokeReply), `"ok":true`) {
		t.Fatalf("connector Agent API invoke: %s %v", invokeReply, err)
	}
	preference, err := st.GetPreference(ctx, "blackboard:group:fixture:connector")
	if err != nil || !preference.Exists {
		t.Fatal("connector invoke not persisted")
	}
	sendConnector(map[string]any{"type": "event", "event": map[string]any{
		"id": "external-event", "type": "session.upsert", "agent_id": "external-agent", "session_id": "external-session",
		"data": map[string]any{"id": "external-session", "agent_id": "external-agent", "title": "External", "workspace": root, "status": "idle", "updated_at": time.Now().UTC().Format(time.RFC3339Nano)}}})
	sendConnector(map[string]any{"type": "ping"})
	if _, _, err := connector.Read(ctx); err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		session, err := st.GetSession(ctx, "external-agent", "external-session")
		if err != nil {
			t.Fatal(err)
		}
		if session != nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("external Connector event not projected")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
