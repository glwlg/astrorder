package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
)

// ChildFactory builds an Adapter for a given connection identity and its settings.
type ChildFactory func(connectionID string, settings map[string]any) (Adapter, error)

// MultiplexRegistry manages dynamic child adapters per connection identity (e.g. SSH).
type MultiplexRegistry struct {
	mu       sync.RWMutex
	factory  ChildFactory
	children map[string]multiplexChild
	sessions map[string]string // sessionID -> connectionID
	states   map[string]Result
	closed   bool
}

type multiplexChild struct {
	canonical string
	adapter   Adapter
}

func NewMultiplexRegistry(factory ChildFactory) *MultiplexRegistry {
	return &MultiplexRegistry{
		factory:  factory,
		children: map[string]multiplexChild{},
		sessions: map[string]string{},
		states:   map[string]Result{},
	}
}

func canonicalJSON(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(b)
	return fmt.Sprintf("%x", hash)
}

func (m *MultiplexRegistry) getOrChild(request Request) (string, Adapter, error) {
	params, _ := request.Fields["params"].(map[string]any)
	if params == nil {
		return "", nil, fmt.Errorf("SSH runtime params are invalid")
	}
	connID, _ := params["connection_id"].(string)
	if connID == "" || len(connID) > 128 {
		return "", nil, fmt.Errorf("SSH connection_id is invalid")
	}
	rawSettings, _ := params["ssh_settings"].(map[string]any)
	if rawSettings == nil || len(rawSettings) == 0 {
		return "", nil, fmt.Errorf("SSH settings are required")
	}

	canonical := canonicalJSON(rawSettings)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", nil, fmt.Errorf("multiplex registry is closed")
	}

	if child, exists := m.children[connID]; exists {
		if child.canonical != canonical {
			return "", nil, fmt.Errorf("SSH connection settings changed while daemon-owned")
		}
		return connID, child.adapter, nil
	}

	adapter, err := m.factory(connID, rawSettings)
	if err != nil {
		return "", nil, err
	}
	m.children[connID] = multiplexChild{
		canonical: canonical,
		adapter:   adapter,
	}
	return connID, adapter, nil
}

func (m *MultiplexRegistry) Create(ctx context.Context, request Request) (Result, error) {
	connID, adapter, err := m.getOrChild(request)
	if err != nil {
		return Result{}, err
	}
	res, err := adapter.Create(ctx, request)
	if err != nil {
		return Result{}, err
	}
	if res.SessionID == "" {
		return Result{}, fmt.Errorf("SSH child did not return a native session ID")
	}
	m.mu.Lock()
	m.sessions[res.SessionID] = connID
	m.states[res.SessionID] = res
	m.mu.Unlock()
	return res, nil
}

func (m *MultiplexRegistry) Spawn(ctx context.Context, request Request) (Result, error) {
	connID, adapter, err := m.getOrChild(request)
	if err != nil {
		return Result{}, err
	}
	spawner, ok := adapter.(Spawner)
	if !ok {
		return Result{}, fmt.Errorf("SSH child does not support spawn")
	}
	res, err := spawner.Spawn(ctx, request)
	if err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	m.sessions[request.SessionID] = connID
	m.states[request.SessionID] = res
	m.mu.Unlock()
	return res, nil
}

func (m *MultiplexRegistry) Command(ctx context.Context, request Request) (Result, error) {
	m.mu.RLock()
	connID, ok := m.sessions[request.SessionID]
	child, hasChild := m.children[connID]
	m.mu.RUnlock()

	if !ok || !hasChild {
		return Result{}, fmt.Errorf("SSH session is not daemon-owned")
	}
	commander, ok := child.adapter.(Commander)
	if !ok {
		return Result{}, fmt.Errorf("SSH child does not support commands")
	}
	result, err := commander.Command(ctx, request)
	if err == nil {
		m.mu.Lock()
		if result.Payload["deleted"] == request.SessionID || result.Payload["closed"] == true {
			delete(m.sessions, request.SessionID)
			delete(m.states, request.SessionID)
		} else if result.Status != "" {
			m.states[request.SessionID] = result
		}
		m.mu.Unlock()
	}
	return result, err
}

// Snapshot forwards only the recorded owner's native state. Command receipts
// are not session snapshots and may omit Agent identity or current activity.
func (m *MultiplexRegistry) Snapshot() map[string]Result {
	m.mu.RLock()
	owners := make(map[string]string, len(m.sessions))
	children := make(map[string]multiplexChild, len(m.children))
	out := make(map[string]Result, len(m.states))
	for id, cid := range m.sessions {
		owners[id] = cid
		out[id] = m.states[id]
	}
	for cid, child := range m.children {
		children[cid] = child
	}
	m.mu.RUnlock()
	for cid, child := range children {
		if reader, ok := child.adapter.(Snapshotter); ok {
			snapshot := reader.Snapshot()
			for id, owner := range owners {
				if owner != cid {
					continue
				}
				if current, found := snapshot[id]; found && current.SessionID == id {
					out[id] = current
				} else {
					out[id] = Result{SessionID: id, Status: "error", Payload: map[string]any{"reconciliation_required": true}}
				}
			}
		}
	}
	return out
}

func (m *MultiplexRegistry) Query(ctx context.Context, request Request) (map[string]any, error) {
	_, adapter, err := m.getOrChild(request)
	if err != nil {
		return nil, err
	}
	querier, ok := adapter.(Querier)
	if !ok {
		return nil, fmt.Errorf("SSH child does not support runtime requests")
	}
	return querier.Query(ctx, request)
}

func (m *MultiplexRegistry) SessionsForConnection(connectionID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for sid, cid := range m.sessions {
		if cid == connectionID {
			out = append(out, sid)
		}
	}
	return out
}

func (m *MultiplexRegistry) DisconnectConnection(ctx context.Context, connectionID string) ([]string, error) {
	m.mu.Lock()
	child, exists := m.children[connectionID]
	if !exists {
		m.mu.Unlock()
		return []string{}, nil
	}
	m.mu.Unlock()

	// 先尝试平滑关闭底层 adapter；若底层关闭失败，不能静默解绑和注销，必须暴露错误并保留状态
	if closer, ok := child.adapter.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			return nil, fmt.Errorf("failed to close connection adapter: %w", err)
		}
	}

	m.mu.Lock()
	delete(m.children, connectionID)
	released := []string{}
	for sid, cid := range m.sessions {
		if cid == connectionID {
			released = append(released, sid)
			delete(m.sessions, sid)
			delete(m.states, sid)
		}
	}
	m.mu.Unlock()

	return released, nil
}

func (m *MultiplexRegistry) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	children := make([]multiplexChild, 0, len(m.children))
	for _, c := range m.children {
		children = append(children, c)
	}
	m.children = map[string]multiplexChild{}
	m.sessions = map[string]string{}
	m.states = map[string]Result{}
	m.mu.Unlock()

	for _, c := range children {
		if closer, ok := c.adapter.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	return nil
}
