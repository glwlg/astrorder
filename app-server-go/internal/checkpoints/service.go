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
	"path"
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

	mu               sync.Mutex
	sessionLocks     map[string]*sync.Mutex
	activePrompt     map[string]activeTurnInfo
	wasRunning       map[string]bool
	pendingBaselines map[string]*pendingBaseline
	baselineWait     map[string]chan struct{}
	turnActivities   map[string]*turnActivity
	lastFinished     map[string]finishedTurnMark

	stopOnce sync.Once
	stopCh   chan struct{}
}

type activeTurnInfo struct {
	CommandID string
	Prompt    string
}

type pendingBaseline struct {
	ID            string
	AgentID       string
	SessionID     string
	CommandID     string
	Workspace     string
	RepoRoot      string
	Branch        string
	HeadSHA       string
	TreeSHA       string
	CommitSHA     string
	RefName       string
	ReuseExisting bool
}

type shellCommandRecord struct {
	Cwd     string
	Command string
}

type turnActivity struct {
	SawMessageEvent   bool
	SawReadOnlyTool   bool
	SawOpaqueMutation bool
	MutatingFilePaths []string
	ShellCommands     []shellCommandRecord
}

func (a *turnActivity) clone() turnActivity {
	if a == nil {
		return turnActivity{}
	}
	out := *a
	if len(a.MutatingFilePaths) > 0 {
		out.MutatingFilePaths = append([]string(nil), a.MutatingFilePaths...)
	}
	if len(a.ShellCommands) > 0 {
		out.ShellCommands = append([]shellCommandRecord(nil), a.ShellCommands...)
	}
	return out
}

type finishedTurnMark struct {
	CommandID string
	At        time.Time
}

func NewService(options Options) *Service {
	svc := &Service{
		options:          options,
		sessionLocks:     map[string]*sync.Mutex{},
		activePrompt:     map[string]activeTurnInfo{},
		wasRunning:       map[string]bool{},
		pendingBaselines: map[string]*pendingBaseline{},
		baselineWait:     map[string]chan struct{}{},
		turnActivities:   map[string]*turnActivity{},
		lastFinished:     map[string]finishedTurnMark{},
		stopCh:           make(chan struct{}),
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
	case "message.upsert", "message.delta":
		s.recordMessageActivity(key, ev.Data)
	case "task.upsert":
		s.recordTaskActivity(key, ev.Data)
	case "command.upsert":
		action := strings.TrimSpace(fmt.Sprint(ev.Data["action"]))
		state := strings.TrimSpace(fmt.Sprint(ev.Data["state"]))
		if action != "send" && action != "enqueue" {
			return
		}
		cmdID := strings.TrimSpace(fmt.Sprint(ev.Data["id"]))
		if cmdID == "<nil>" {
			cmdID = ""
		}
		text := strings.TrimSpace(fmt.Sprint(ev.Data["text"]))
		if text == "<nil>" {
			text = ""
		}
		if state == "accepted" || state == "running" {
			s.startTurn(key, ev.AgentID, ev.SessionID, cmdID, truncateLabel(text))
		} else if state == "completed" {
			s.finishTurn(key, ev.AgentID, ev.SessionID, cmdID, truncateLabel(text), false)
		}
	case "session.upsert":
		status := strings.TrimSpace(fmt.Sprint(ev.Data["status"]))
		if status == "running" {
			s.startTurn(key, ev.AgentID, ev.SessionID, "", "")
		} else if status == "idle" {
			s.finishTurn(key, ev.AgentID, ev.SessionID, "", "", true)
		}
	case "codex.notification":
		frame, _ := ev.Data["frame"].(map[string]any)
		if frame == nil {
			frame = ev.Data
		}
		method := strings.TrimSpace(fmt.Sprint(frame["method"]))
		switch method {
		case "turn/started":
			s.startTurn(key, ev.AgentID, ev.SessionID, "", "")
		case "turn/completed":
			s.finishTurn(key, ev.AgentID, ev.SessionID, "", "", false)
		case "item/started", "item/completed":
			s.recordCodexItemActivity(key, frame)
		}
	case "hermes.command_complete", "grok.completed":
		cmdID := strings.TrimSpace(fmt.Sprint(ev.Data["command_id"]))
		if cmdID == "<nil>" {
			cmdID = ""
		}
		s.finishTurn(key, ev.AgentID, ev.SessionID, cmdID, "", false)
	}
}

func (s *Service) startTurn(key, agentID, sessionID, cmdID, prompt string) {
	s.mu.Lock()
	already := s.wasRunning[key]
	if cmdID != "" || prompt != "" {
		info := s.activePrompt[key]
		if cmdID != "" {
			info.CommandID = cmdID
		}
		if prompt != "" {
			info.Prompt = prompt
		}
		s.activePrompt[key] = info
	}
	s.wasRunning[key] = true
	if already {
		s.mu.Unlock()
		return
	}
	s.turnActivities[key] = &turnActivity{}
	done := make(chan struct{})
	s.baselineWait[key] = done
	s.mu.Unlock()

	go func() {
		defer close(done)
		s.beginTurnBaseline(key, agentID, sessionID, cmdID)
	}()
}

func (s *Service) finishTurn(key, agentID, sessionID, cmdID, fallbackPrompt string, requireWasRunning bool) {
	s.mu.Lock()
	was := s.wasRunning[key]
	if requireWasRunning && !was {
		s.mu.Unlock()
		return
	}
	if !was {
		if prev, ok := s.lastFinished[key]; ok {
			if (cmdID != "" && prev.CommandID == cmdID) || time.Since(prev.At) < 3*time.Second {
				s.mu.Unlock()
				return
			}
		}
	}
	info := s.activePrompt[key]
	delete(s.wasRunning, key)
	waitCh := s.baselineWait[key]
	delete(s.baselineWait, key)
	act := s.turnActivities[key].clone()
	delete(s.turnActivities, key)
	effectiveCmdID := cmdID
	if effectiveCmdID == "" {
		effectiveCmdID = info.CommandID
	}
	label := info.Prompt
	if label == "" {
		label = fallbackPrompt
	}
	s.lastFinished[key] = finishedTurnMark{CommandID: effectiveCmdID, At: time.Now()}
	s.mu.Unlock()

	go s.finishTurnCapture(key, agentID, sessionID, effectiveCmdID, label, waitCh, act)
}

func (s *Service) beginTurnBaseline(key, agentID, sessionID, commandID string) {
	ctx, cancel := context.WithTimeout(context.Background(), autoCaptureLimit)
	defer cancel()
	enabled, autoCapture, _ := s.resolveRuntimePolicy(ctx)
	if !enabled || !autoCapture {
		return
	}
	resolved, resolvedAgentID, _, err := s.resolveSessionFS(ctx, agentID, sessionID)
	if err != nil {
		return
	}
	if resolvedAgentID == "" {
		resolvedAgentID = agentID
	}

	lock := s.lockFor(resolvedAgentID, sessionID)
	lock.Lock()
	defer lock.Unlock()

	latest, err := s.options.Store.LatestSessionCheckpoint(ctx, resolvedAgentID, sessionID)
	if err != nil {
		return
	}
	checkpointID := newCheckpointID()
	refName := buildCheckpointRef(resolvedAgentID, sessionID, checkpointID)
	res, err := resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
		Operation: targetfs.GitCheckpointCapture,
		Workspace: resolved.Workspace,
		Branch:    refName,
	})
	if err != nil {
		return
	}
	wire, err := targetfs.ParseCheckpointCapture(res.Stdout)
	if err != nil {
		return
	}

	next := &pendingBaseline{
		ID:        checkpointID,
		AgentID:   resolvedAgentID,
		SessionID: sessionID,
		CommandID: commandID,
		Workspace: resolved.Workspace,
		RepoRoot:  wire.RepoRoot,
		Branch:    wire.Branch,
		HeadSHA:   wire.Head,
		TreeSHA:   wire.TreeSHA,
		CommitSHA: wire.CommitSHA,
		RefName:   wire.RefName,
	}
	if latest != nil && latest.TreeSHA == wire.TreeSHA {
		_, _ = resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
			Operation: targetfs.GitCheckpointDelete,
			Workspace: resolved.Workspace,
			Branch:    wire.RefName,
		})
		next.ReuseExisting = true
		next.RefName = ""
	}

	s.mu.Lock()
	old := s.pendingBaselines[key]
	s.pendingBaselines[key] = next
	s.mu.Unlock()

	if old != nil && !old.ReuseExisting && old.RefName != "" {
		_, _ = resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
			Operation: targetfs.GitCheckpointDelete,
			Workspace: old.Workspace,
			Branch:    old.RefName,
		})
	}
}

func (s *Service) finishTurnCapture(
	key, agentID, sessionID, commandID, label string,
	waitCh chan struct{},
	activity turnActivity,
) {
	ctx, cancel := context.WithTimeout(context.Background(), autoCaptureLimit)
	defer cancel()
	if waitCh != nil {
		select {
		case <-waitCh:
		case <-ctx.Done():
			return
		}
	}

	s.mu.Lock()
	baseline := s.pendingBaselines[key]
	delete(s.pendingBaselines, key)
	s.mu.Unlock()

	cleanupBaseline := func(resolved *targetfs.Resolved) {
		if baseline == nil || baseline.ReuseExisting || baseline.RefName == "" {
			return
		}
		if resolved != nil {
			_, _ = resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
				Operation: targetfs.GitCheckpointDelete,
				Workspace: resolved.Workspace,
				Branch:    baseline.RefName,
			})
		}
	}

	enabled, autoCapture, _ := s.resolveRuntimePolicy(ctx)
	if !enabled || !autoCapture {
		if baseline != nil && !baseline.ReuseExisting && baseline.RefName != "" {
			if resolved, _, _, err := s.resolveSessionFS(ctx, agentID, sessionID); err == nil {
				cleanupBaseline(&resolved)
			}
		}
		return
	}

	resolved, resolvedAgentID, _, err := s.resolveSessionFS(ctx, agentID, sessionID)
	if err != nil {
		return
	}
	if resolvedAgentID == "" {
		resolvedAgentID = agentID
	}

	lock := s.lockFor(resolvedAgentID, sessionID)
	lock.Lock()
	defer lock.Unlock()

	repoRootHint := ""
	if baseline != nil {
		repoRootHint = baseline.RepoRoot
	}
	if !activityAllowsWorkspaceCheckpoint(activity, resolved.Workspace, repoRootHint) {
		cleanupBaseline(&resolved)
		return
	}

	latest, err := s.options.Store.LatestSessionCheckpoint(ctx, resolvedAgentID, sessionID)
	if err != nil {
		cleanupBaseline(&resolved)
		return
	}

	compareFrom := ""
	if baseline != nil {
		compareFrom = baseline.TreeSHA
	} else if latest != nil {
		compareFrom = latest.TreeSHA
	}

	checkpointID := newCheckpointID()
	refName := buildCheckpointRef(resolvedAgentID, sessionID, checkpointID)
	res, err := resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
		Operation:  targetfs.GitCheckpointCapture,
		Workspace:  resolved.Workspace,
		Branch:     refName,
		BaseBranch: compareFrom,
	})
	if err != nil {
		cleanupBaseline(&resolved)
		return
	}
	wire, err := targetfs.ParseCheckpointCapture(res.Stdout)
	if err != nil {
		cleanupBaseline(&resolved)
		return
	}

	// Re-check activity once the canonical RepoRoot is known (in case baseline was absent).
	if repoRootHint == "" && !activityAllowsWorkspaceCheckpoint(activity, resolved.Workspace, wire.RepoRoot) {
		_, _ = resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
			Operation: targetfs.GitCheckpointDelete,
			Workspace: resolved.Workspace,
			Branch:    wire.RefName,
		})
		cleanupBaseline(&resolved)
		return
	}

	// If the workspace tree at turn end is identical to turn start (or latest when
	// no turn baseline was captured), this turn made zero changes to the repository.
	unchangedFromBaseline := baseline != nil && wire.TreeSHA == baseline.TreeSHA
	unchangedFromLatest := baseline == nil && latest != nil && wire.TreeSHA == latest.TreeSHA
	if unchangedFromBaseline || unchangedFromLatest {
		_, _ = resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{
			Operation: targetfs.GitCheckpointDelete,
			Workspace: resolved.Workspace,
			Branch:    wire.RefName,
		})
		cleanupBaseline(&resolved)
		return
	}

	// Promote the pre-turn baseline as a clean restore anchor (never blaming
	// prior external commits from other sessions on this turn's baseline).
	if baseline != nil && !baseline.ReuseExisting {
		_, _ = s.options.Store.InsertSessionCheckpoint(ctx, store.SessionCheckpoint{
			ID:           baseline.ID,
			AgentID:      resolvedAgentID,
			SessionID:    sessionID,
			Kind:         "auto_before",
			CommandID:    baseline.CommandID,
			Label:        "本轮开始前基线",
			Workspace:    resolved.Workspace,
			RepoRoot:     baseline.RepoRoot,
			Branch:       baseline.Branch,
			HeadSHA:      baseline.HeadSHA,
			TreeSHA:      baseline.TreeSHA,
			CommitSHA:    baseline.CommitSHA,
			RefName:      baseline.RefName,
			PrevTreeSHA:  "",
			FilesChanged: 0,
			Insertions:   0,
			Deletions:    0,
			Files:        []store.SessionCheckpointFile{},
		})
	}

	finalLabel := strings.TrimSpace(label)
	if finalLabel == "" {
		finalLabel = "本轮执行完成后"
	}
	saved, err := s.options.Store.InsertSessionCheckpoint(ctx, store.SessionCheckpoint{
		ID:           checkpointID,
		AgentID:      resolvedAgentID,
		SessionID:    sessionID,
		Kind:         "auto_after",
		CommandID:    commandID,
		Label:        truncateLabel(finalLabel),
		Workspace:    resolved.Workspace,
		RepoRoot:     wire.RepoRoot,
		Branch:       wire.Branch,
		HeadSHA:      wire.Head,
		TreeSHA:      wire.TreeSHA,
		CommitSHA:    wire.CommitSHA,
		RefName:      wire.RefName,
		PrevTreeSHA:  compareFrom,
		FilesChanged: wire.FilesChanged,
		Insertions:   wire.Insertions,
		Deletions:    wire.Deletions,
		Files:        convertFiles(wire.Files),
	})
	if err != nil {
		return
	}
	s.pruneAutoCheckpoints(ctx, resolved, resolvedAgentID, sessionID)
	s.publish(ctx, "checkpoints.updated", map[string]any{
		"agent_id":      saved.AgentID,
		"session_id":    saved.SessionID,
		"action":        "create",
		"checkpoint_id": saved.ID,
	})
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

func (s *Service) recordMessageActivity(key string, data map[string]any) {
	if data == nil {
		return
	}
	role := strings.ToLower(strings.TrimSpace(fmt.Sprint(data["role"])))
	kind := strings.ToLower(strings.TrimSpace(fmt.Sprint(data["kind"])))

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.wasRunning[key] {
		return
	}
	act := s.turnActivities[key]
	if act == nil {
		act = &turnActivity{}
		s.turnActivities[key] = act
	}
	if role == "tool" || kind == "tool" {
		if toolMap, ok := data["tool"].(map[string]any); ok {
			name := strings.TrimSpace(fmt.Sprint(toolMap["name"]))
			recordToolIntoActivity(act, name, toolMap)
		} else {
			act.SawOpaqueMutation = true
		}
		return
	}
	if role == "assistant" {
		act.SawMessageEvent = true
	}
}

func (s *Service) recordTaskActivity(key string, data map[string]any) {
	if data == nil {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(fmt.Sprint(data["kind"])))
	if kind != "tool" && kind != "subagent" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.wasRunning[key] {
		return
	}
	act := s.turnActivities[key]
	if act == nil {
		act = &turnActivity{}
		s.turnActivities[key] = act
	}
	if kind == "subagent" {
		act.SawOpaqueMutation = true
		return
	}
	progress, _ := data["progress"].(map[string]any)
	name := ""
	if progress != nil {
		if t, ok := progress["tool"].(string); ok && strings.TrimSpace(t) != "" {
			name = strings.TrimSpace(t)
		}
	}
	if name == "" {
		if title, ok := data["title"].(string); ok {
			name = strings.TrimSpace(title)
		}
	}
	payload := map[string]any{}
	if progress != nil {
		for k, v := range progress {
			payload[k] = v
		}
	}
	if cmd, ok := data["command"]; ok && cmd != nil {
		payload["command"] = cmd
	}
	recordToolIntoActivity(act, name, payload)
}

func (s *Service) recordCodexItemActivity(key string, frame map[string]any) {
	params, _ := frame["params"].(map[string]any)
	if params == nil {
		return
	}
	item, _ := params["item"].(map[string]any)
	if item == nil {
		return
	}
	itemType := strings.TrimSpace(fmt.Sprint(item["type"]))
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.wasRunning[key] {
		return
	}
	act := s.turnActivities[key]
	if act == nil {
		act = &turnActivity{}
		s.turnActivities[key] = act
	}
	switch strings.ToLower(itemType) {
	case "agentmessage", "reasoning", "usermessage":
		act.SawMessageEvent = true
	case "filechange", "patch":
		recordToolIntoActivity(act, itemType, item)
	case "commandexecution":
		recordToolIntoActivity(act, itemType, item)
	case "collabagenttoolcall", "subagentactivity", "mcptoolcall":
		act.SawOpaqueMutation = true
	default:
		if itemType != "" && itemType != "<nil>" {
			recordToolIntoActivity(act, itemType, item)
		}
	}
}

func recordToolIntoActivity(act *turnActivity, rawName string, payload map[string]any) {
	name := strings.ToLower(strings.TrimSpace(rawName))
	if name == "" || name == "<nil>" {
		act.SawOpaqueMutation = true
		return
	}
	switch name {
	case "read_file", "fileread", "view_file", "view_file_outline", "view_code_item",
		"list_dir", "find_by_name", "grep_search", "grep", "search_files", "filesearch",
		"searchcontext", "search_web", "web_search", "read_url_content", "open_browser_url",
		"image_view", "imageview", "read_resource", "list_resources",
		"ask_question", "requestuserinput", "read_board", "post_message", "swarm_wait",
		"command_status", "thinking", "reasoning", "todowrite", "update_plan", "read":
		act.SawReadOnlyTool = true
		return
	case "write_file", "write_to_file", "edit", "edit_file", "replace_file_content",
		"multi_replace_file_content", "sed_file", "notebook_edit", "filechange", "patch", "apply_patch":
		paths := extractToolPaths(payload)
		if len(paths) == 0 {
			act.SawOpaqueMutation = true
			return
		}
		act.MutatingFilePaths = append(act.MutatingFilePaths, paths...)
		return
	case "run_command", "commandexecution", "bash", "sh", "exec", "exec_command", "terminal":
		cwd, cmd := extractShellInvocation(payload)
		if cwd == "" {
			act.SawOpaqueMutation = true
			return
		}
		act.ShellCommands = append(act.ShellCommands, shellCommandRecord{Cwd: cwd, Command: cmd})
		return
	default:
		act.SawOpaqueMutation = true
	}
}

func extractToolPaths(payload map[string]any) []string {
	if payload == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(raw any) {
		s, ok := raw.(string)
		if !ok {
			return
		}
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	scanMap := func(m map[string]any) {
		for _, key := range []string{"Path", "path", "TargetFile", "target_file", "file_path", "filePath", "AbsolutePath", "absolute_path", "filename", "file"} {
			if val, ok := m[key]; ok {
				add(val)
			}
		}
	}
	scanMap(payload)
	if args, ok := payload["arguments"].(map[string]any); ok {
		scanMap(args)
	} else if argsStr, ok := payload["arguments"].(string); ok && strings.HasPrefix(strings.TrimSpace(argsStr), "{") {
		var parsed map[string]any
		if json.Unmarshal([]byte(argsStr), &parsed) == nil {
			scanMap(parsed)
		}
	}
	if locs, ok := payload["locations"].([]any); ok {
		for _, item := range locs {
			if m, ok := item.(map[string]any); ok {
				scanMap(m)
			} else {
				add(item)
			}
		}
	}
	if changes, ok := payload["changes"].([]any); ok {
		for _, item := range changes {
			if m, ok := item.(map[string]any); ok {
				scanMap(m)
			}
		}
	} else if changesMap, ok := payload["changes"].(map[string]any); ok {
		for k := range changesMap {
			add(k)
		}
	}
	if diffs, ok := payload["diff"].([]any); ok {
		for _, item := range diffs {
			if m, ok := item.(map[string]any); ok {
				scanMap(m)
			}
		}
	}
	return out
}

func extractShellInvocation(payload map[string]any) (cwd, command string) {
	if payload == nil {
		return "", ""
	}
	readFields := func(m map[string]any) {
		if cwd == "" {
			for _, key := range []string{"Cwd", "cwd", "workdir", "working_directory", "WorkingDirectory", "dir"} {
				if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
					cwd = strings.TrimSpace(v)
					break
				}
			}
		}
		if command == "" {
			for _, key := range []string{"CommandLine", "command", "cmd"} {
				if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
					command = strings.TrimSpace(v)
					break
				}
			}
		}
	}
	readFields(payload)
	if args, ok := payload["arguments"].(map[string]any); ok {
		readFields(args)
	} else if argsStr, ok := payload["arguments"].(string); ok && strings.HasPrefix(strings.TrimSpace(argsStr), "{") {
		var parsed map[string]any
		if json.Unmarshal([]byte(argsStr), &parsed) == nil {
			readFields(parsed)
		}
	}
	return cwd, command
}

func activityAllowsWorkspaceCheckpoint(act turnActivity, workspace, repoRoot string) bool {
	if act.SawOpaqueMutation {
		return true
	}
	observedTelemetry := act.SawMessageEvent || act.SawReadOnlyTool || len(act.MutatingFilePaths) > 0 || len(act.ShellCommands) > 0
	if !observedTelemetry {
		// Minimal connectors or tests that do not stream per-turn message/tool
		// telemetry fall back safely to pure tree comparison.
		return true
	}
	for _, p := range act.MutatingFilePaths {
		if pathTouchesWorkspace(p, workspace, repoRoot) {
			return true
		}
	}
	for _, sh := range act.ShellCommands {
		if pathTouchesWorkspace(sh.Cwd, workspace, repoRoot) || commandMentionsWorkspace(sh.Command, workspace, repoRoot) {
			return true
		}
	}
	return false
}

func normalizeComparePath(raw string) (normalized string, isAbs bool) {
	s := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if s == "" {
		return "", false
	}
	// Normalize Windows drive paths (e.g. C:/foo -> /mnt/c/foo) so WSL and
	// Windows path notations match cleanly.
	if len(s) >= 2 && s[1] == ':' && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		drive := strings.ToLower(string(s[0]))
		rest := strings.TrimPrefix(s[2:], "/")
		return path.Clean("/mnt/" + drive + "/" + rest), true
	}
	if strings.HasPrefix(s, "/") {
		cleaned := path.Clean(s)
		if strings.HasPrefix(strings.ToLower(cleaned), "/mnt/") && len(cleaned) >= 6 {
			parts := strings.SplitN(cleaned, "/", 4)
			if len(parts) >= 3 && len(parts[2]) == 1 {
				parts[2] = strings.ToLower(parts[2])
				cleaned = strings.Join(parts, "/")
			}
		}
		return cleaned, true
	}
	return path.Clean(s), false
}

func pathTouchesWorkspace(target, workspace, repoRoot string) bool {
	normTarget, isAbs := normalizeComparePath(target)
	if normTarget == "" {
		return true
	}
	if !isAbs {
		// Relative paths resolve against the session workspace.
		return true
	}
	for _, root := range []string{workspace, repoRoot} {
		normRoot, rootAbs := normalizeComparePath(root)
		if !rootAbs || normRoot == "" || normRoot == "/" {
			continue
		}
		if normTarget == normRoot || strings.HasPrefix(normTarget, normRoot+"/") {
			return true
		}
		// Case-insensitive fallback for Windows / WSL /mnt/<drive> paths.
		if strings.HasPrefix(normRoot, "/mnt/") && strings.HasPrefix(normTarget, "/mnt/") {
			lt := strings.ToLower(normTarget)
			lr := strings.ToLower(normRoot)
			if lt == lr || strings.HasPrefix(lt, lr+"/") {
				return true
			}
		}
	}
	return false
}

func commandMentionsWorkspace(command, workspace, repoRoot string) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	lowerCmd := strings.ToLower(strings.ReplaceAll(command, "\\", "/"))
	for _, root := range []string{workspace, repoRoot} {
		normRoot, isAbs := normalizeComparePath(root)
		if !isAbs || normRoot == "" || normRoot == "/" {
			continue
		}
		if strings.Contains(lowerCmd, strings.ToLower(normRoot)) {
			return true
		}
		base := strings.ToLower(path.Base(normRoot))
		if len(base) >= 3 && strings.Contains(lowerCmd, base) {
			return true
		}
	}
	return false
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
