// Package checkpoints captures, compares, and safely restores per-turn Git
// workspace checkpoints across local, WSL, and SSH sessions.
package checkpoints

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	maxJSONBody              = 256 * 1024
	autoCaptureLimit         = 25 * time.Second
	defaultMaxAutoPerSession = 20
	minMaxAutoPerSession     = 1
	maxMaxAutoPerSession     = 200
	pluginID                 = "code-checkpoints"
	prefPluginsEnabled       = "plugins:enabled"
	prefPluginsConfig        = "plugins:config"
	configKeyMaxAuto         = "maxAutoSnapshots"
	configKeyAutoCapture     = "autoCapture"
)

type preferenceReader interface {
	GetPreference(context.Context, string) (store.Preference, error)
}

type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type Publisher interface {
	Publish(context.Context, string, map[string]any) error
}

type EventSubscriber interface {
	Subscribe() (<-chan *store.Event, func())
}

type Store interface {
	FindSessionByID(context.Context, string) (map[string]any, error)
	ListSessionCheckpoints(context.Context, string, string, int) ([]store.SessionCheckpoint, error)
	GetSessionCheckpoint(context.Context, string, string, string) (*store.SessionCheckpoint, error)
	LatestSessionCheckpoint(context.Context, string, string) (*store.SessionCheckpoint, error)
	InsertSessionCheckpoint(context.Context, store.SessionCheckpoint) (store.SessionCheckpoint, error)
	DeleteSessionCheckpoint(context.Context, string, string, string) (bool, error)
	ListPrunableAutoCheckpoints(context.Context, string, string, int) ([]store.SessionCheckpoint, error)
}

type Options struct {
	Store             Store
	Authorizer        BrowserAuthorizer
	Resolver          *targetfs.Resolver
	Publisher         Publisher
	Subscriber        EventSubscriber
	MaxAutoPerSession int
}

type Service struct {
	options Options

	mu           sync.Mutex
	sessionLocks map[string]*sync.Mutex
	activePrompt map[string]activeTurnInfo
	wasRunning   map[string]bool

	stopOnce sync.Once
	stopCh   chan struct{}
}

type activeTurnInfo struct {
	CommandID string
	Prompt    string
}

func NewService(options Options) *Service {
	svc := &Service{
		options:      options,
		sessionLocks: map[string]*sync.Mutex{},
		activePrompt: map[string]activeTurnInfo{},
		wasRunning:   map[string]bool{},
		stopCh:       make(chan struct{}),
	}
	if options.Subscriber != nil && options.Store != nil && options.Resolver != nil {
		go svc.watchEvents()
	}
	return svc
}

func (s *Service) Close() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
}

func Matches(method, path string) bool {
	if !strings.HasPrefix(path, "/api/v1/sessions/") {
		return false
	}
	_, action, _ := parseRoute(path)
	switch action {
	case "list":
		return method == http.MethodGet || method == http.MethodPost
	case "diff":
		return method == http.MethodGet
	case "restore":
		return method == http.MethodPost
	case "item":
		return method == http.MethodDelete
	default:
		return false
	}
}

func parseRoute(path string) (sessionID, action, checkpointID string) {
	trimmed := strings.TrimPrefix(path, "/api/v1/sessions/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != "checkpoints" {
		return "", "", ""
	}
	sid, err := url.PathUnescape(parts[0])
	if err != nil || strings.TrimSpace(sid) == "" {
		return "", "", ""
	}
	switch len(parts) {
	case 2:
		return sid, "list", ""
	case 3:
		if parts[2] == "diff" {
			return sid, "diff", ""
		}
		cid, err := url.PathUnescape(parts[2])
		if err != nil || strings.TrimSpace(cid) == "" {
			return "", "", ""
		}
		return sid, "item", cid
	case 4:
		if parts[3] != "restore" {
			return "", "", ""
		}
		cid, err := url.PathUnescape(parts[2])
		if err != nil || strings.TrimSpace(cid) == "" {
			return "", "", ""
		}
		return sid, "restore", cid
	default:
		return "", "", ""
	}
}

func (s *Service) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if s.options.Authorizer == nil {
		writeError(response, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := s.options.Authorizer.AuthorizeBrowser(request); failure != nil {
		writeError(response, failure.Status, failure.Detail)
		return
	}
	if s.options.Store == nil || s.options.Resolver == nil {
		writeError(response, http.StatusServiceUnavailable, "Checkpoints service is not configured")
		return
	}

	sessionID, action, checkpointID := parseRoute(request.URL.Path)
	if sessionID == "" {
		writeError(response, http.StatusNotFound, "Not Found")
		return
	}

	var err error
	switch {
	case action == "list" && request.Method == http.MethodGet:
		err = s.handleList(response, request, sessionID)
	case action == "list" && request.Method == http.MethodPost:
		err = s.handleCreate(response, request, sessionID)
	case action == "diff" && request.Method == http.MethodGet:
		err = s.handleDiff(response, request, sessionID)
	case action == "restore" && request.Method == http.MethodPost:
		err = s.handleRestore(response, request, sessionID, checkpointID)
	case action == "item" && request.Method == http.MethodDelete:
		err = s.handleDelete(response, request, sessionID, checkpointID)
	default:
		writeError(response, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	if err != nil {
		writeTargetError(response, err)
	}
}

func (s *Service) handleList(response http.ResponseWriter, request *http.Request, sessionID string) error {
	agentID := strings.TrimSpace(request.URL.Query().Get("agent_id"))
	_, _, maxAuto := s.resolveRuntimePolicy(request.Context())
	listLimit := maxAuto + 30
	if listLimit < 80 {
		listLimit = 80
	}
	if listLimit > 250 {
		listLimit = 250
	}
	items, err := s.options.Store.ListSessionCheckpoints(request.Context(), agentID, sessionID, listLimit)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (s *Service) handleCreate(response http.ResponseWriter, request *http.Request, sessionID string) error {
	var payload struct {
		AgentID   string `json:"agent_id"`
		Label     string `json:"label"`
		Kind      string `json:"kind"`
		CommandID string `json:"command_id"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	kind := strings.TrimSpace(payload.Kind)
	if kind == "" {
		kind = "manual"
	}
	item, created, err := s.Capture(request.Context(), strings.TrimSpace(payload.AgentID), sessionID, kind, strings.TrimSpace(payload.CommandID), strings.TrimSpace(payload.Label), false)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"checkpoint": item, "created": created})
	return nil
}

func (s *Service) handleDiff(response http.ResponseWriter, request *http.Request, sessionID string) error {
	query := request.URL.Query()
	fromTree := strings.TrimSpace(query.Get("from"))
	toTree := strings.TrimSpace(query.Get("to"))
	if toTree == "" {
		toTree = "working"
	}
	if fromTree == "" {
		return &targetfs.Error{Status: 400, Code: "from_required", Detail: "from tree SHA is required"}
	}
	resolved, _, _, err := s.resolveSessionFS(request.Context(), strings.TrimSpace(query.Get("agent_id")), sessionID)
	if err != nil {
		return err
	}
	res, err := resolved.FileSystem.ExecGit(request.Context(), targetfs.GitRequest{
		Operation:  targetfs.GitCheckpointDiff,
		Workspace:  resolved.Workspace,
		BaseBranch: fromTree,
		Branch:     toTree,
		Path:       strings.TrimSpace(query.Get("path")),
	})
	if err != nil {
		return err
	}
	wire, err := targetfs.ParseCheckpointDiff(res.Stdout)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, wire)
	return nil
}

func (s *Service) handleRestore(response http.ResponseWriter, request *http.Request, sessionID, checkpointID string) error {
	var payload struct {
		AgentID string `json:"agent_id"`
		Path    string `json:"path"`
	}
	_ = decodeJSON(response, request, &payload)
	agentID := strings.TrimSpace(payload.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(request.URL.Query().Get("agent_id"))
	}
	restorePath := strings.TrimSpace(payload.Path)

	target, err := s.options.Store.GetSessionCheckpoint(request.Context(), agentID, sessionID, checkpointID)
	if err != nil {
		return err
	}
	if target == nil {
		return &targetfs.Error{Status: 404, Code: "checkpoint_not_found", Detail: "检查点不存在"}
	}

	lock := s.lockFor(target.AgentID, target.SessionID)
	lock.Lock()
	defer lock.Unlock()

	resolved, _, _, err := s.resolveSessionFS(request.Context(), target.AgentID, target.SessionID)
	if err != nil {
		return err
	}

	backupID := newCheckpointID()
	backupRef := buildCheckpointRef(target.AgentID, target.SessionID, backupID)
	res, err := resolved.FileSystem.ExecGit(request.Context(), targetfs.GitRequest{
		Operation:  targetfs.GitCheckpointRestore,
		Workspace:  resolved.Workspace,
		BaseBranch: target.TreeSHA,
		Branch:     backupRef,
		Path:       restorePath,
	})
	if err != nil {
		return err
	}
	wire, err := targetfs.ParseCheckpointRestore(res.Stdout)
	if err != nil {
		return err
	}

	var savedBackup *store.SessionCheckpoint
	if wire.Backup != nil {
		backupLabel := fmt.Sprintf("回滚至 #%d 前自动备份", target.TurnNumber)
		if restorePath != "" {
			backupLabel = truncateLabel(fmt.Sprintf("恢复单文件 %s 至 #%d 前备份", restorePath, target.TurnNumber))
		}
		backupRecord := store.SessionCheckpoint{
			ID:           backupID,
			AgentID:      target.AgentID,
			SessionID:    target.SessionID,
			Kind:         "pre_restore",
			Label:        backupLabel,
			Workspace:    resolved.Workspace,
			RepoRoot:     wire.Backup.RepoRoot,
			Branch:       wire.Backup.Branch,
			HeadSHA:      wire.Backup.Head,
			TreeSHA:      wire.Backup.TreeSHA,
			CommitSHA:    wire.Backup.CommitSHA,
			RefName:      wire.Backup.RefName,
			PrevTreeSHA:  target.TreeSHA,
			FilesChanged: wire.Backup.FilesChanged,
			Insertions:   wire.Backup.Insertions,
			Deletions:    wire.Backup.Deletions,
			Files:        convertFiles(wire.Backup.Files),
		}
		if inserted, insertErr := s.options.Store.InsertSessionCheckpoint(request.Context(), backupRecord); insertErr == nil {
			savedBackup = &inserted
		}
	}

	s.publish(request.Context(), "checkpoints.updated", map[string]any{
		"agent_id":      target.AgentID,
		"session_id":    target.SessionID,
		"action":        "restore",
		"checkpoint_id": target.ID,
		"path":          restorePath,
	})
	s.publish(request.Context(), "files.changed", map[string]any{
		"action":        "checkpoint_restore",
		"path":          resolved.Workspace,
		"connection_id": resolved.ConnectionID,
	})

	writeJSON(response, http.StatusOK, map[string]any{
		"ok":            true,
		"restored":      target,
		"restored_path": restorePath,
		"removed_files": wire.RemovedFiles,
		"backup":        savedBackup,
	})
	return nil
}

func (s *Service) handleDelete(response http.ResponseWriter, request *http.Request, sessionID, checkpointID string) error {
	agentID := strings.TrimSpace(request.URL.Query().Get("agent_id"))
	target, err := s.options.Store.GetSessionCheckpoint(request.Context(), agentID, sessionID, checkpointID)
	if err != nil {
		return err
	}
	if target == nil {
		return &targetfs.Error{Status: 404, Code: "checkpoint_not_found", Detail: "检查点不存在"}
	}
	if target.RefName != "" {
		if resolved, _, _, rErr := s.resolveSessionFS(request.Context(), target.AgentID, target.SessionID); rErr == nil {
			_, _ = resolved.FileSystem.ExecGit(request.Context(), targetfs.GitRequest{
				Operation: targetfs.GitCheckpointDelete,
				Workspace: resolved.Workspace,
				Branch:    target.RefName,
			})
		}
	}
	if _, err := s.options.Store.DeleteSessionCheckpoint(request.Context(), target.AgentID, target.SessionID, target.ID); err != nil {
		return err
	}
	s.publish(request.Context(), "checkpoints.updated", map[string]any{
		"agent_id":      target.AgentID,
		"session_id":    target.SessionID,
		"action":        "delete",
		"checkpoint_id": target.ID,
	})
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// Capture records a workspace checkpoint. When dedupIfUnchanged is true and the
// latest checkpoint already has the exact same tree SHA, no duplicate row is stored.
func (s *Service) Capture(
	ctx context.Context,
	agentID, sessionID, kind, commandID, label string,
	dedupIfUnchanged bool,
) (store.SessionCheckpoint, bool, error) {
	resolved, resolvedAgentID, sessionTitle, err := s.resolveSessionFS(ctx, agentID, sessionID)
	if err != nil {
		return store.SessionCheckpoint{}, false, err
	}
	if resolvedAgentID == "" {
		resolvedAgentID = agentID
	}

	lock := s.lockFor(resolvedAgentID, sessionID)
	lock.Lock()
	defer lock.Unlock()

	latest, err := s.options.Store.LatestSessionCheckpoint(ctx, resolvedAgentID, sessionID)
	if err != nil {
		return store.SessionCheckpoint{}, false, err
	}
	prevTree := ""
	if latest != nil {
		prevTree = latest.TreeSHA
	}

	checkpointID := newCheckpointID()
	refName := buildCheckpointRef(resolvedAgentID, sessionID, checkpointID)
	res, err := resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
		Operation:  targetfs.GitCheckpointCapture,
		Workspace:  resolved.Workspace,
		Branch:     refName,
		BaseBranch: prevTree,
	})
	if err != nil {
		return store.SessionCheckpoint{}, false, err
	}
	wire, err := targetfs.ParseCheckpointCapture(res.Stdout)
	if err != nil {
		return store.SessionCheckpoint{}, false, err
	}

	if dedupIfUnchanged && latest != nil && latest.TreeSHA == wire.TreeSHA {
		_, _ = resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
			Operation: targetfs.GitCheckpointDelete,
			Workspace: resolved.Workspace,
			Branch:    wire.RefName,
		})
		return *latest, false, nil
	}

	if label == "" {
		switch kind {
		case "auto_before":
			label = "本轮开始前基线"
		case "auto_after":
			label = "本轮执行完成后"
		case "manual":
			if sessionTitle != "" {
				label = "手动快照"
			} else {
				label = "手动检查点"
			}
		default:
			label = "工作区检查点"
		}
	}

	saved, err := s.options.Store.InsertSessionCheckpoint(ctx, store.SessionCheckpoint{
		ID:           checkpointID,
		AgentID:      resolvedAgentID,
		SessionID:    sessionID,
		Kind:         kind,
		CommandID:    commandID,
		Label:        truncateLabel(label),
		Workspace:    resolved.Workspace,
		RepoRoot:     wire.RepoRoot,
		Branch:       wire.Branch,
		HeadSHA:      wire.Head,
		TreeSHA:      wire.TreeSHA,
		CommitSHA:    wire.CommitSHA,
		RefName:      wire.RefName,
		PrevTreeSHA:  prevTree,
		FilesChanged: wire.FilesChanged,
		Insertions:   wire.Insertions,
		Deletions:    wire.Deletions,
		Files:        convertFiles(wire.Files),
	})
	if err != nil {
		return store.SessionCheckpoint{}, false, err
	}
	if kind == "auto_before" || kind == "auto_after" {
		s.pruneAutoCheckpoints(ctx, resolved, resolvedAgentID, sessionID)
	}
	s.publish(ctx, "checkpoints.updated", map[string]any{
		"agent_id":      saved.AgentID,
		"session_id":    saved.SessionID,
		"action":        "create",
		"checkpoint_id": saved.ID,
	})
	return saved, true, nil
}

func (s *Service) resolveRuntimePolicy(ctx context.Context) (enabled bool, autoCapture bool, maxAuto int) {
	enabled = true
	autoCapture = true
	maxAuto = s.options.MaxAutoPerSession
	if maxAuto <= 0 {
		maxAuto = defaultMaxAutoPerSession
	}
	reader, ok := s.options.Store.(preferenceReader)
	if !ok || reader == nil {
		return enabled, autoCapture, maxAuto
	}
	if pref, err := reader.GetPreference(ctx, prefPluginsEnabled); err == nil && pref.Exists {
		if m, ok := pref.Value.(map[string]any); ok {
			if raw, exists := m[pluginID]; exists {
				if flag, ok := raw.(bool); ok {
					enabled = flag
				}
			}
		}
	}
	if pref, err := reader.GetPreference(ctx, prefPluginsConfig); err == nil && pref.Exists {
		if m, ok := pref.Value.(map[string]any); ok {
			if cfg, ok := m[pluginID].(map[string]any); ok {
				if rawAuto, exists := cfg[configKeyAutoCapture]; exists {
					if flag, ok := rawAuto.(bool); ok {
						autoCapture = flag
					}
				}
				if rawLimit, exists := cfg[configKeyMaxAuto]; exists {
					if parsed, ok := parsePositiveInt(rawLimit); ok {
						if parsed < minMaxAutoPerSession {
							parsed = minMaxAutoPerSession
						} else if parsed > maxMaxAutoPerSession {
							parsed = maxMaxAutoPerSession
						}
						maxAuto = parsed
					}
				}
			}
		}
	}
	return enabled, autoCapture, maxAuto
}

func parsePositiveInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		if v > 0 {
			return v, true
		}
	case int64:
		if v > 0 {
			return int(v), true
		}
	case float64:
		if v >= 1 {
			return int(v), true
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n), true
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n, true
		}
	}
	return 0, false
}

func (s *Service) pruneAutoCheckpoints(ctx context.Context, resolved targetfs.Resolved, agentID, sessionID string) {
	_, _, limit := s.resolveRuntimePolicy(ctx)
	prunable, err := s.options.Store.ListPrunableAutoCheckpoints(ctx, agentID, sessionID, limit)
	if err != nil || len(prunable) == 0 {
		return
	}
	for _, old := range prunable {
		if old.RefName != "" {
			_, _ = resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
				Operation: targetfs.GitCheckpointDelete,
				Workspace: resolved.Workspace,
				Branch:    old.RefName,
			})
		}
		_, _ = s.options.Store.DeleteSessionCheckpoint(ctx, old.AgentID, old.SessionID, old.ID)
	}
}

func (s *Service) watchEvents() {
	ch, unsubscribe := s.options.Subscriber.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-s.stopCh:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev == nil || ev.AgentID == "" || ev.SessionID == "" {
				continue
			}
			s.handleEvent(ev)
		}
	}
}

func (s *Service) handleEvent(ev *store.Event) {
	key := ev.AgentID + "::" + ev.SessionID
	switch ev.Type {
	case "command.upsert":
		action := strings.TrimSpace(fmt.Sprint(ev.Data["action"]))
		state := strings.TrimSpace(fmt.Sprint(ev.Data["state"]))
		if action != "send" && action != "enqueue" {
			return
		}
		cmdID := strings.TrimSpace(fmt.Sprint(ev.Data["id"]))
		text := strings.TrimSpace(fmt.Sprint(ev.Data["text"]))
		if text == "<nil>" {
			text = ""
		}
		if state == "accepted" || state == "running" {
			s.mu.Lock()
			already := s.wasRunning[key]
			s.activePrompt[key] = activeTurnInfo{CommandID: cmdID, Prompt: truncateLabel(text)}
			s.wasRunning[key] = true
			s.mu.Unlock()
			if !already {
				go s.captureBackground(ev.AgentID, ev.SessionID, "auto_before", cmdID, "")
			}
		} else if state == "completed" {
			s.mu.Lock()
			info := s.activePrompt[key]
			delete(s.wasRunning, key)
			s.mu.Unlock()
			label := info.Prompt
			if label == "" {
				label = truncateLabel(text)
			}
			go s.captureBackground(ev.AgentID, ev.SessionID, "auto_after", cmdID, label)
		}
	case "session.upsert":
		status := strings.TrimSpace(fmt.Sprint(ev.Data["status"]))
		if status == "running" {
			s.mu.Lock()
			already := s.wasRunning[key]
			s.wasRunning[key] = true
			s.mu.Unlock()
			if !already {
				go s.captureBackground(ev.AgentID, ev.SessionID, "auto_before", "", "")
			}
		} else if status == "idle" {
			s.mu.Lock()
			was := s.wasRunning[key]
			info := s.activePrompt[key]
			delete(s.wasRunning, key)
			s.mu.Unlock()
			if was {
				go s.captureBackground(ev.AgentID, ev.SessionID, "auto_after", info.CommandID, info.Prompt)
			}
		}
	case "codex.notification":
		frame, _ := ev.Data["frame"].(map[string]any)
		if frame == nil {
			frame = ev.Data
		}
		method := strings.TrimSpace(fmt.Sprint(frame["method"]))
		if method == "turn/started" {
			s.mu.Lock()
			already := s.wasRunning[key]
			s.wasRunning[key] = true
			s.mu.Unlock()
			if !already {
				go s.captureBackground(ev.AgentID, ev.SessionID, "auto_before", "", "")
			}
		} else if method == "turn/completed" {
			s.mu.Lock()
			info := s.activePrompt[key]
			delete(s.wasRunning, key)
			s.mu.Unlock()
			go s.captureBackground(ev.AgentID, ev.SessionID, "auto_after", info.CommandID, info.Prompt)
		}
	case "hermes.command_complete", "grok.completed":
		s.mu.Lock()
		info := s.activePrompt[key]
		delete(s.wasRunning, key)
		s.mu.Unlock()
		cmdID := strings.TrimSpace(fmt.Sprint(ev.Data["command_id"]))
		if cmdID == "" || cmdID == "<nil>" {
			cmdID = info.CommandID
		}
		go s.captureBackground(ev.AgentID, ev.SessionID, "auto_after", cmdID, info.Prompt)
	}
}

func (s *Service) captureBackground(agentID, sessionID, kind, commandID, label string) {
	ctx, cancel := context.WithTimeout(context.Background(), autoCaptureLimit)
	defer cancel()
	enabled, autoCapture, _ := s.resolveRuntimePolicy(ctx)
	if !enabled || !autoCapture {
		return
	}
	_, _, _ = s.Capture(ctx, agentID, sessionID, kind, commandID, label, true)
}

func (s *Service) resolveSessionFS(ctx context.Context, agentID, sessionID string) (targetfs.Resolved, string, string, error) {
	session, err := s.options.Store.FindSessionByID(ctx, sessionID)
	if err != nil {
		return targetfs.Resolved{}, "", "", err
	}
	if session == nil {
		return targetfs.Resolved{}, "", "", &targetfs.Error{Status: 404, Code: "session_not_found", Detail: "会话不存在"}
	}
	workspace := strings.TrimSpace(fmt.Sprint(session["workspace"]))
	if workspace == "" || workspace == "<nil>" {
		return targetfs.Resolved{}, "", "", &targetfs.Error{Status: 422, Code: "workspace_required", Detail: "该会话未绑定 Git 工作区"}
	}
	resolvedAgentID := strings.TrimSpace(fmt.Sprint(session["agent_id"]))
	if resolvedAgentID == "" || resolvedAgentID == "<nil>" {
		resolvedAgentID = agentID
	}
	connectionID := strings.TrimSpace(fmt.Sprint(session["connection_id"]))
	if connectionID == "<nil>" {
		connectionID = ""
	}
	title := strings.TrimSpace(fmt.Sprint(session["title"]))
	if title == "<nil>" {
		title = ""
	}
	resolved, err := s.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    workspace,
		SessionID:    sessionID,
		ConnectionID: connectionID,
	})
	if err != nil {
		return targetfs.Resolved{}, "", "", err
	}
	return resolved, resolvedAgentID, title, nil
}

func (s *Service) lockFor(agentID, sessionID string) *sync.Mutex {
	key := agentID + "::" + sessionID
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.sessionLocks[key]
	if !ok {
		lock = &sync.Mutex{}
		s.sessionLocks[key] = lock
	}
	return lock
}

func (s *Service) publish(ctx context.Context, eventType string, data map[string]any) {
	if s.options.Publisher != nil {
		_ = s.options.Publisher.Publish(ctx, eventType, data)
	}
}

func buildCheckpointRef(agentID, sessionID, checkpointID string) string {
	sum := sha256.Sum256([]byte(agentID + "::" + sessionID))
	return "refs/astrorder/checkpoints/" + hex.EncodeToString(sum[:8]) + "/" + checkpointID
}

func newCheckpointID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "cp-" + hex.EncodeToString(buf)
}

func truncateLabel(raw string) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	runes := []rune(cleaned)
	if len(runes) > 96 {
		return string(runes[:96]) + "…"
	}
	return cleaned
}

func convertFiles(src []targetfs.CheckpointFileStat) []store.SessionCheckpointFile {
	out := make([]store.SessionCheckpointFile, 0, len(src))
	for _, f := range src {
		out = append(out, store.SessionCheckpointFile{
			Path:       f.Path,
			Status:     f.Status,
			Insertions: f.Insertions,
			Deletions:  f.Deletions,
		})
	}
	return out
}

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	if request.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, maxJSONBody))
	if err := decoder.Decode(destination); err != nil && !errors.Is(err, io.EOF) {
		return &targetfs.Error{Status: 400, Code: "invalid_json", Detail: "Invalid JSON", Err: err}
	}
	return nil
}

func writeTargetError(response http.ResponseWriter, err error) {
	var target *targetfs.Error
	if errors.As(err, &target) {
		writeError(response, target.Status, target.Detail)
		return
	}
	writeError(response, http.StatusInternalServerError, err.Error())
}

func writeError(response http.ResponseWriter, status int, detail string) {
	writeJSON(response, status, map[string]string{"detail": detail})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
