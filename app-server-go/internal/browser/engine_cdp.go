package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// scopedCDP exposes only this task's targets to an upstream browser engine.
type scopedCDP struct {
	mu       sync.Mutex
	primary  string
	targets  map[string]bool
	sessions map[string]bool
	pending  map[string]string
	events   []map[string]any
	onTarget func(string)
	active   bool
}

func (s *scopedCDP) ownsTarget(id string) bool  { return id != "" && s.targets[id] }
func (s *scopedCDP) ownsSession(id string) bool { return id != "" && s.sessions[id] }

func (s *scopedCDP) allowRequest(frame map[string]any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	method, _ := frame["method"].(string)
	session, _ := frame["sessionId"].(string)
	params, _ := frame["params"].(map[string]any)
	if session != "" && !s.ownsSession(session) {
		return false
	}
	target, _ := params["targetId"].(string)
	switch method {
	case "Target.getTargets":
		s.pending[fmt.Sprint(frame["id"])] = method
		return true
	case "Target.setDiscoverTargets":
		return true
	case "Target.setAutoAttach":
		return params["waitForDebuggerOnStart"] == false && params["flatten"] == true
	case "Target.attachToTarget", "Target.activateTarget", "Target.getTargetInfo":
		return s.ownsTarget(target)
	case "Target.closeTarget":
		return target != s.primary && s.ownsTarget(target)
	case "Target.detachFromTarget":
		id, _ := params["sessionId"].(string)
		return s.ownsSession(id)
	case "Target.createTarget":
		if session != "" || params["browserContextId"] != nil || params["forTab"] != nil {
			return false
		}
		s.pending[fmt.Sprint(frame["id"])] = method
		return true
	case "Browser.getVersion":
		return true
	case "Browser.getWindowForTarget":
		return s.ownsTarget(target)
	}
	if len(method) >= 7 && method[:7] == "Target." {
		return false
	}
	if len(method) >= 8 && method[:8] == "Browser." {
		return false
	}
	return session != ""
}

// filterResponse returns frames for the client, including any events held
// while Chrome was creating a target requested by this task.
func (s *scopedCDP) filterResponse(frame map[string]any) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id := frame["id"]; id != nil {
		key := fmt.Sprint(id)
		method := s.pending[key]
		delete(s.pending, key)
		result, _ := frame["result"].(map[string]any)
		if method == "Target.getTargets" && result != nil {
			infos, _ := result["targetInfos"].([]any)
			filtered := make([]any, 0, len(infos))
			for _, entry := range infos {
				info, _ := entry.(map[string]any)
				target, _ := info["targetId"].(string)
				if s.ownsTarget(target) {
					filtered = append(filtered, entry)
				}
			}
			result["targetInfos"] = filtered
		}
		out := []map[string]any{frame}
		if method == "Target.createTarget" {
			if result != nil {
				target, _ := result["targetId"].(string)
				s.addTarget(target, true)
			}
			var creating bool
			for _, pending := range s.pending {
				creating = creating || pending == "Target.createTarget"
			}
			held := s.events
			s.events = nil
			for _, event := range held {
				if s.allowEvent(event, false) {
					out = append(out, event)
				} else if creating {
					s.events = append(s.events, event)
				}
			}
		}
		return out
	}
	if s.allowEvent(frame, true) {
		return []map[string]any{frame}
	}
	return nil
}

func (s *scopedCDP) addTarget(target string, page bool) {
	if target == "" || s.targets[target] {
		return
	}
	s.targets[target] = true
	if page && s.onTarget != nil {
		go s.onTarget(target)
	}
}

func (s *scopedCDP) allowEvent(frame map[string]any, mayHold bool) bool {
	method, _ := frame["method"].(string)
	parentSession, _ := frame["sessionId"].(string)
	params, _ := frame["params"].(map[string]any)
	info, _ := params["targetInfo"].(map[string]any)
	target, _ := info["targetId"].(string)
	if target == "" {
		target, _ = params["targetId"].(string)
	}
	opener, _ := info["openerId"].(string)
	kind, _ := info["type"].(string)
	if !s.ownsTarget(target) && (s.ownsTarget(opener) || s.ownsSession(parentSession)) {
		s.addTarget(target, kind == "page")
	}
	switch method {
	case "Target.attachedToTarget":
		if s.ownsTarget(target) {
			id, _ := params["sessionId"].(string)
			if id != "" {
				s.sessions[id] = true
			}
			return true
		}
	case "Target.targetCreated", "Target.targetInfoChanged", "Target.targetDestroyed":
		if s.ownsTarget(target) {
			return true
		}
	case "Target.detachedFromTarget":
		id, _ := params["sessionId"].(string)
		if s.ownsSession(id) {
			delete(s.sessions, id)
			return true
		}
	default:
		return s.ownsSession(parentSession)
	}
	if mayHold && target != "" && len(s.events) < 128 {
		for _, pending := range s.pending {
			if pending == "Target.createTarget" {
				s.events = append(s.events, frame)
				break
			}
		}
	}
	return false
}

func startScopedCDP(ctx context.Context, upstreamURL, target string, onTarget func(string)) (string, *scopedCDP, func(), error) {
	if upstreamURL == "" || target == "" {
		return "", nil, nil, errors.New("浏览器会话不可用")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, nil, err
	}
	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		listener.Close()
		return "", nil, nil, err
	}
	path := "/" + hex.EncodeToString(token)
	state := &scopedCDP{
		primary: target, targets: map[string]bool{target: true}, sessions: map[string]bool{},
		pending: map[string]string{}, onTarget: onTarget,
	}
	server := &http.Server{}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		state.mu.Lock()
		if state.active {
			state.mu.Unlock()
			http.Error(w, "浏览器引擎已连接", http.StatusConflict)
			return
		}
		state.active = true
		state.sessions = map[string]bool{}
		state.pending = map[string]string{}
		state.events = nil
		state.mu.Unlock()
		defer func() {
			state.mu.Lock()
			state.active = false
			state.mu.Unlock()
		}()
		client, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer client.Close(websocket.StatusNormalClosure, "")
		upstream, _, err := websocket.Dial(ctx, upstreamURL, nil)
		if err != nil {
			return
		}
		defer upstream.Close(websocket.StatusNormalClosure, "")
		client.SetReadLimit(64 << 20)
		upstream.SetReadLimit(64 << 20)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				_, data, err := upstream.Read(ctx)
				if err != nil {
					return
				}
				var frame map[string]any
				if json.Unmarshal(data, &frame) != nil {
					continue
				}
				for _, filtered := range state.filterResponse(frame) {
					raw, err := json.Marshal(filtered)
					if err != nil || client.Write(ctx, websocket.MessageText, raw) != nil {
						return
					}
				}
			}
		}()
		for {
			_, data, err := client.Read(ctx)
			if err != nil {
				break
			}
			var frame map[string]any
			if json.Unmarshal(data, &frame) != nil {
				continue
			}
			if !state.allowRequest(frame) {
				response, _ := json.Marshal(map[string]any{
					"id": frame["id"], "error": map[string]any{"code": -32000, "message": "目标不属于当前浏览器会话"},
				})
				if client.Write(ctx, websocket.MessageText, response) != nil {
					break
				}
				continue
			}
			if upstream.Write(ctx, websocket.MessageText, data) != nil {
				break
			}
		}
		upstream.Close(websocket.StatusNormalClosure, "")
		<-done
	})
	go server.Serve(listener)
	stop := func() { _ = server.Close(); _ = listener.Close() }
	return "ws://" + listener.Addr().String() + path, state, stop, nil
}

func (m *SessionManager) rememberEngineTarget(ctx context.Context, sessionKey, target string) {
	m.targetMu.Lock()
	for owner, targets := range m.sessionTargets {
		if owner != sessionKey && ownsTarget(targets, target) {
			m.targetMu.Unlock()
			return
		}
	}
	if !ownsTarget(m.sessionTargets[sessionKey], target) {
		m.sessionTargets[sessionKey] = append(m.sessionTargets[sessionKey], target)
	}
	m.targetMu.Unlock()
	pages, err := m.endpoints.ListPages(ctx)
	if err != nil {
		return
	}
	for _, page := range pages {
		if page.ID == target {
			_ = m.tagPage(ctx, &page, sessionKey)
			return
		}
	}
}
