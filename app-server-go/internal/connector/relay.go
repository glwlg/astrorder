// Package connector bridges the public Connector WebSocket to typed daemon ingress.
package connector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

type InvokeFunc func(context.Context, string, map[string]any) (any, error)
type connection struct {
	socket  *websocket.Conn
	writeMu sync.Mutex
	sent    map[string]bool
}
type Relay struct {
	Client      *daemonclient.Client
	Store       *store.Store
	Authorizer  *auth.Authenticator
	Invoke      InvokeFunc
	mu          sync.Mutex
	connections map[string]*connection
}

var agentKind = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

func (c *connection) send(ctx context.Context, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.socket.Write(writeCtx, websocket.MessageText, body)
}
func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if failure := r.Authorizer.AuthorizeConnector(req); failure != nil {
		http.Error(w, failure.Detail, failure.Status)
		return
	}
	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(daemonclient.MaxCompatBusinessBytes)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	unregister := r.Client.RegisterSubscription(cancel)
	defer unregister()
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	_, body, err := conn.Read(readCtx)
	readCancel()
	if err != nil {
		return
	}
	hello, err := decodeObject(body)
	if err != nil || hello["type"] != "hello" || hello["protocol_version"] != json.Number("1") {
		_ = conn.Close(websocket.StatusPolicyViolation, "Invalid connector hello")
		return
	}
	agent, ok := hello["agent"].(map[string]any)
	if !ok {
		_ = conn.Close(websocket.StatusPolicyViolation, "Invalid Agent")
		return
	}
	id, _ := agent["id"].(string)
	kind, _ := agent["kind"].(string)
	name, _ := agent["name"].(string)
	state, _ := agent["status"].(string)
	if id == "" || len(id) > 256 || !agentKind.MatchString(kind) || len(kind) > 64 || name == "" || len(name) > 256 || (state != "ready" && state != "connecting" && state != "disconnected" && state != "error") {
		_ = conn.Close(websocket.StatusPolicyViolation, "Invalid Agent")
		return
	}
	caps := []string{}
	if raw, exists := agent["capabilities"]; exists {
		list, ok := raw.([]any)
		if !ok || len(list) > 32 {
			_ = conn.Close(websocket.StatusPolicyViolation, "Invalid capabilities")
			return
		}
		for _, value := range list {
			s, ok := value.(string)
			if !ok {
				_ = conn.Close(websocket.StatusPolicyViolation, "Invalid capability")
				return
			}
			caps = append(caps, s)
		}
	}
	c := &connection{socket: conn, sent: make(map[string]bool)}
	r.mu.Lock()
	if r.connections == nil {
		r.connections = make(map[string]*connection)
	}
	if r.connections[id] != nil {
		r.mu.Unlock()
		_ = conn.Close(websocket.StatusPolicyViolation, "Agent already registered")
		return
	}
	r.connections[id] = c
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.connections[id] == c {
			delete(r.connections, id)
		}
		r.mu.Unlock()
	}()
	stream, err := r.Client.Connector.Connect(ctx)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "Daemon connector unavailable")
		return
	}
	extra := make(map[string]any)
	for key, value := range agent {
		if key != "id" && key != "kind" && key != "name" && key != "status" && key != "capabilities" {
			extra[key] = value
		}
	}
	native, _ := json.Marshal(extra)
	if err = stream.Send(&sessiondv1.ConnectorIngressFrame{Frame: &sessiondv1.ConnectorIngressFrame_Hello{Hello: &sessiondv1.ConnectorHello{ProtocolVersion: 1, Agent: &sessiondv1.ConnectorAgent{Id: id, Kind: kind, Name: name, Status: state, Capabilities: caps, Native: &sessiondv1.NativeJson{Schema: "astrorder.connector.agent.v1", Utf8Json: native}}}}}); err != nil {
		return
	}
	ack, err := stream.Recv()
	if err != nil || ack.GetRegistered() == nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "Daemon registration refused: "+status.Code(err).String())
		return
	}
	digest := sha256.Sum256([]byte(id))
	streamID := fmt.Sprintf("connector-%x", digest[:24])
	bound, err := r.Client.Connector.BindControlSession(ctx, &sessiondv1.BindControlSessionRequest{AgentId: id, ControlSession: &sessiondv1.SessionRef{SessionId: streamID, AgentId: &id}})
	if err != nil || !bound.GetBound() {
		_ = conn.Close(websocket.StatusInternalError, "Daemon connector binding failed")
		return
	}
	if err = r.dispatch(ctx, id, c); err != nil {
		return
	}
	for {
		_, body, err = conn.Read(ctx)
		if err != nil {
			return
		}
		frame, decodeErr := decodeObject(body)
		if decodeErr != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "Invalid connector frame")
			return
		}
		switch frame["type"] {
		case "ping":
			if err = c.send(ctx, map[string]any{"type": "pong"}); err != nil {
				return
			}
		case "pull":
			if err = r.dispatch(ctx, id, c); err != nil {
				return
			}
		case "event":
			event, ok := frame["event"].(map[string]any)
			if !ok {
				_ = conn.Close(websocket.StatusPolicyViolation, "Invalid connector event")
				return
			}
			if err = r.forward(ctx, stream, id, event); err != nil {
				_ = c.send(ctx, map[string]any{"type": "error", "detail": "Connector event was rejected"})
				return
			}
		case "agent_invoke":
			capability, _ := frame["capability"].(string)
			input, _ := frame["input"].(map[string]any)
			if input == nil {
				input = map[string]any{}
			}
			input["caller_agent_id"] = id
			if r.Invoke == nil {
				_ = c.send(ctx, map[string]any{"type": "agent_result", "id": frame["id"], "ok": false, "error": map[string]any{"code": "unavailable", "message": "Agent API is unavailable"}})
				continue
			}
			data, invokeErr := r.Invoke(ctx, capability, input)
			result := map[string]any{"type": "agent_result", "id": frame["id"], "ok": invokeErr == nil, "data": data}
			if invokeErr != nil {
				delete(result, "data")
				result["error"] = map[string]any{"code": "invalid_input", "message": invokeErr.Error()}
			}
			if err = c.send(ctx, result); err != nil {
				return
			}
		default:
			if err = c.send(ctx, map[string]any{"type": "error", "detail": "Unsupported connector frame"}); err != nil {
				return
			}
		}
	}
}
func (r *Relay) dispatch(ctx context.Context, agent string, c *connection) error {
	commands, err := r.Store.PendingCommands(ctx, agent)
	if err != nil {
		return err
	}
	for _, command := range commands {
		session, _ := command["session_id"].(string)
		id, _ := command["id"].(string)
		key := session + "\x00" + id
		c.writeMu.Lock()
		sent := c.sent[key]
		c.writeMu.Unlock()
		if sent {
			continue
		}
		if err = c.send(ctx, map[string]any{"type": "command", "command": command}); err != nil {
			return err
		}
		c.writeMu.Lock()
		c.sent[key] = true
		c.writeMu.Unlock()
	}
	return nil
}
func (r *Relay) forward(ctx context.Context, stream grpc.BidiStreamingClient[sessiondv1.ConnectorIngressFrame, sessiondv1.ConnectorIngressAck], agentID string, event map[string]any) error {
	id, _ := event["id"].(string)
	kind, _ := event["type"].(string)
	agent, _ := event["agent_id"].(string)
	data, ok := event["data"].(map[string]any)
	if id == "" || len(id) > 256 || agent != agentID || !ok {
		return errors.New("invalid event identity")
	}
	switch kind {
	case "agent.upsert", "session.upsert", "session.delete", "message.upsert", "task.upsert", "command.upsert", "approval.upsert":
	default:
		return errors.New("unsupported event")
	}
	var session *string
	if raw, exists := event["session_id"]; exists && raw != nil {
		sid, ok := raw.(string)
		if !ok || sid == "" || len(sid) > 256 {
			return errors.New("invalid event session")
		}
		session = &sid
	}
	if kind == "agent.upsert" {
		if data["id"] != agentID {
			return errors.New("Agent mismatch")
		}
	} else {
		if session == nil {
			return errors.New("session required")
		}
		if raw, exists := data["agent_id"]; exists && raw != agentID {
			return errors.New("Agent mismatch")
		}
		if raw, exists := data["session_id"]; exists && raw != *session {
			return errors.New("session mismatch")
		}
		if kind == "session.upsert" && data["id"] != *session {
			return errors.New("session mismatch")
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	payload := &sessiondv1.ConnectorEvent{EventId: id, EventType: kind, AgentId: agentID, SessionId: session, Data: &sessiondv1.NativeJson{Schema: "astrorder.connector.event.data.v1", Utf8Json: raw}}
	if kind == "session.upsert" {
		statuses := map[string]sessiondv1.SessionStatus{"idle": sessiondv1.SessionStatus_SESSION_STATUS_IDLE, "running": sessiondv1.SessionStatus_SESSION_STATUS_RUNNING, "waiting_approval": sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL, "error": sessiondv1.SessionStatus_SESSION_STATUS_ERROR}
		state, _ := data["status"].(string)
		value, valid := statuses[state]
		if !valid {
			return errors.New("invalid session status")
		}
		payload.Status = &value
	}
	if err = stream.Send(&sessiondv1.ConnectorIngressFrame{Frame: &sessiondv1.ConnectorIngressFrame_Event{Event: payload}}); err != nil {
		return err
	}
	ack, err := stream.Recv()
	if err != nil {
		return err
	}
	if ack.GetEventAccepted() == nil {
		return errors.New("daemon rejected event")
	}
	return nil
}
func decodeObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("JSON object required")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return value, nil
}
