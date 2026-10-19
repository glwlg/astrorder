package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// nativeCall uses only the owned process's loopback service. Redirects and
// ambient proxy settings are disabled; native diagnostics are never exposed.
func (a *Adapter) nativeCall(ctx context.Context, s *session, method string, p map[string]any) (map[string]any, error) {
	s.mu.Lock()
	endpoint, token := s.nativeURL, s.nativeToken
	s.mu.Unlock()
	if endpoint == "" {
		if err := a.discoverNative(ctx, s); err != nil {
			return nil, core.RejectBeforeDispatch(err.Error())
		}
		s.mu.Lock()
		endpoint = s.nativeURL
		s.mu.Unlock()
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/exa.language_server_pb.LanguageServerService/"+method, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Codeium-Csrf-Token", token)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Antigravity native %s returned HTTP %d", method, resp.StatusCode)
	}
	var out map[string]any
	dec := json.NewDecoder(io.LimitReader(resp.Body, 16_000_001))
	if err = dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *Adapter) discoverNative(ctx context.Context, s *session) error {
	s.mu.Lock()
	cmd, token, id := s.cmd, s.nativeToken, s.id
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("Antigravity native control owner is unavailable")
	}
	ports, err := nativePorts(cmd.Process.Pid)
	if err != nil {
		return err
	}
	for _, port := range ports {
		endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
		raw, _ := json.Marshal(map[string]any{"conversationId": id})
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		req, _ := http.NewRequestWithContext(probeCtx, http.MethodPost, endpoint+"/exa.language_server_pb.LanguageServerService/GetConversationMetadata", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Codeium-Csrf-Token", token)
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, e := client.Do(req)
		valid := false
		if e == nil {
			var metadata map[string]any
			e = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&metadata)
			resp.Body.Close()
			valid = resp.StatusCode == 200 && e == nil && metadata["metadata"] != nil
		}
		client.CloseIdleConnections()
		cancel()
		if valid {
			s.mu.Lock()
			s.nativeURL = endpoint
			s.mu.Unlock()
			return nil
		}
	}
	return fmt.Errorf("Antigravity native control service is unavailable")
}

func (a *Adapter) interrupt(ctx context.Context, s *session) (core.Result, error) {
	s.mu.Lock()
	valid := !s.control && !s.readonly && !s.closed && s.status == "running"
	s.mu.Unlock()
	if !valid {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity interrupt requires an active owned conversation")
	}
	if _, err := a.nativeCall(ctx, s, "CancelCascadeInvocation", map[string]any{"cascadeId": s.id, "killBackgroundTasks": true}); err != nil {
		return core.Result{}, err
	}
	result := a.result(s)
	result.Payload["accepted"] = true
	// The stream's terminal result, never this RPC receipt, settles the send.
	return result, nil
}

func (a *Adapter) fork(ctx context.Context, p map[string]any) (core.Result, error) {
	id := str(p, "parent_session_id")
	a.mu.Lock()
	s := a.sessions[id]
	a.mu.Unlock()
	if s == nil {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity fork parent is not owned")
	}
	s.mu.Lock()
	if s.control || s.readonly || s.closed || s.deletePending || s.status != "idle" {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity fork requires an idle owned conversation")
	}
	cwd, model, effort, permission := s.cwd, s.model, s.effort, s.permission
	s.status = "running"
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if !s.closed && s.status == "running" {
			s.status = "idle"
		}
		s.mu.Unlock()
	}()
	if requested := str(p, "cwd"); requested != "" {
		var err error
		cwd, err = a.workspace(requested)
		if err != nil {
			return core.Result{}, core.RejectBeforeDispatch(err.Error())
		}
	}
	meta, err := a.nativeCall(ctx, s, "GetConversationMetadata", map[string]any{"conversationId": id})
	if err != nil {
		return core.Result{}, err
	}
	count, _ := meta["numSteps"].(float64)
	index := int(count) - 1
	if boundary := str(p, "target_message_id"); boundary != "" {
		prefix := "antigravity:" + id + ":"
		if !strings.HasPrefix(boundary, prefix) {
			return core.Result{}, core.RejectBeforeDispatch("Antigravity fork boundary belongs to another conversation")
		}
		index, err = strconv.Atoi(strings.TrimPrefix(boundary, prefix))
		if err != nil {
			return core.Result{}, core.RejectBeforeDispatch("invalid Antigravity fork boundary")
		}
	}
	if index < 0 || index >= int(count) {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity fork boundary is outside native history")
	}
	native, err := a.nativeCall(ctx, s, "ForkConversation", map[string]any{"sourceCascadeId": id, "forkAtStepIndex": index, "targetForkWorkspace": "FORK_TARGET_CURRENT_WORKSPACE"})
	if err != nil {
		return core.Result{}, err
	}
	child := str(native, "newCascadeId")
	if !validID(child) || child == id {
		return core.Result{}, fmt.Errorf("Antigravity did not confirm fork identity; reconcile before retry")
	}
	params := map[string]any{"cwd": cwd, "model": model, "effort": effort, "permission_mode": permission}
	for _, key := range []string{"model", "effort"} {
		if v := str(p, key); v != "" {
			params[key] = v
		}
	}
	result, err := a.open(ctx, child, params)
	if err != nil {
		return core.Result{}, fmt.Errorf("Antigravity fork %s created but attach failed; reconcile before retry: %w", child, err)
	}
	result.Payload["parent_session_id"] = id
	return result, nil
}
