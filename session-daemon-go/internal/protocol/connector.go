package protocol

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

func (daemon *Daemon) serveConnector(w http.ResponseWriter, r *http.Request) {
	daemon.mu.Lock()
	secret := daemon.connectorSecret
	daemon.mu.Unlock()

	if secret == "" {
		http.Error(w, "connector endpoint not configured", http.StatusUnauthorized)
		return
	}

	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	supplied := strings.TrimPrefix(auth, prefix)
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer socket.CloseNow()
	socket.SetReadLimit(maxMessageBytes)

	ctx := r.Context()
	var agentID string

	defer func() {
		if agentID != "" {
			daemon.mu.Lock()
			delete(daemon.connectors, agentID)
			daemon.mu.Unlock()
		}
	}()

	for {
		typ, data, err := socket.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}

		var frame map[string]any
		if err := json.Unmarshal(data, &frame); err != nil {
			_ = socket.Close(websocket.StatusPolicyViolation, "invalid JSON")
			return
		}

		frameType, _ := frame["type"].(string)
		if agentID == "" {
			if frameType != "hello" {
				_ = socket.Close(websocket.StatusPolicyViolation, "connector hello is required")
				return
			}
			protocolVersion, _ := frame["protocol_version"].(float64)
			if int(protocolVersion) != 1 {
				_ = socket.Close(websocket.StatusPolicyViolation, "unsupported protocol version")
				return
			}
			agent, _ := frame["agent"].(map[string]any)
			id, _ := agent["id"].(string)
			if id == "" {
				_ = socket.Close(websocket.StatusPolicyViolation, "invalid agent id")
				return
			}
			agentID = id
			daemon.mu.Lock()
			daemon.connectors[agentID] = agent
			daemon.mu.Unlock()
			daemon.recordConnectorHello(agentID, agent)
			continue
		}

		if frameType == "ping" {
			pong, _ := json.Marshal(map[string]any{"type": "pong"})
			if err := socket.Write(ctx, websocket.MessageText, pong); err != nil {
				return
			}
			continue
		}

		if frameType == "event" {
			event, _ := frame["event"].(map[string]any)
			if event == nil {
				_ = socket.Close(websocket.StatusPolicyViolation, "connector event is invalid")
				return
			}
			if err := daemon.recordConnectorEvent(agentID, event); err != nil {
				_ = socket.Close(websocket.StatusPolicyViolation, err.Error())
				return
			}
			continue
		}

		_ = socket.Close(websocket.StatusPolicyViolation, "connector frame type is unsupported")
		return
	}
}

func (daemon *Daemon) recordConnectorHello(agentID string, agent map[string]any) {
	daemon.mu.Lock()
	controlSessionID, hasControl := daemon.agentControls[agentID]
	if !hasControl {
		daemon.pendingEvents[agentID] = append(daemon.pendingEvents[agentID], map[string]any{
			"_daemon_event": "connector.hello",
			"agent":         agent,
		})
		daemon.mu.Unlock()
		return
	}
	daemon.mu.Unlock()

	_ = daemon.Emit(controlSessionID, "connector.hello", agent, "idle")
}

func (daemon *Daemon) recordConnectorEvent(agentID string, event map[string]any) error {
	eventAgentID, _ := event["agent_id"].(string)
	if eventAgentID != agentID {
		return newProtocolError("connector event agent identity does not match hello")
	}
	eventType, _ := event["type"].(string)
	if eventType == "" {
		return newProtocolError("connector event type is invalid")
	}
	sessionID, hasSession := event["session_id"].(string)

	daemon.mu.Lock()
	if eventType == "agent.upsert" {
		if data, ok := event["data"].(map[string]any); ok {
			if curr, exists := daemon.connectors[agentID]; exists {
				for k, v := range data {
					curr[k] = v
				}
			}
		}
	}

	if hasSession && sessionID != "" {
		daemon.mu.Unlock()
		status := "idle"
		if eventType == "session.upsert" {
			if data, ok := event["data"].(map[string]any); ok {
				if s, ok := data["status"].(string); ok && s != "" {
					status = s
				}
			}
		}
		_ = daemon.Emit(sessionID, "connector.event", event, status)
		return nil
	}

	controlSessionID, hasControl := daemon.agentControls[agentID]
	if !hasControl {
		daemon.pendingEvents[agentID] = append(daemon.pendingEvents[agentID], event)
		daemon.mu.Unlock()
		return nil
	}
	daemon.mu.Unlock()

	_ = daemon.Emit(controlSessionID, "connector.event", event, "idle")
	return nil
}

// BindAgentControlSession binds an active agent_id to a session acting as control plane.
func (daemon *Daemon) BindAgentControlSession(controlSessionID string, metadata map[string]any) {
	agentID, _ := metadata["agent_id"].(string)
	if agentID == "" || controlSessionID == "" {
		return
	}

	daemon.mu.Lock()
	daemon.agentControls[agentID] = controlSessionID
	pending := daemon.pendingEvents[agentID]
	delete(daemon.pendingEvents, agentID)
	daemon.mu.Unlock()

	for _, ev := range pending {
		if ev["_daemon_event"] == "connector.hello" {
			if agent, ok := ev["agent"].(map[string]any); ok {
				_ = daemon.Emit(controlSessionID, "connector.hello", agent, "idle")
			}
		} else {
			_ = daemon.Emit(controlSessionID, "connector.event", ev, "idle")
		}
	}
}

type protocolError struct {
	msg string
}

func (e *protocolError) Error() string { return e.msg }

func newProtocolError(msg string) error {
	return &protocolError{msg: msg}
}
